package persistence

import (
	"context"
	"database/sql"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	"sort"
	"time"
)

type CompanyDecisionPlan struct {
	Key          string            `json:"decision_key"`
	Suggested    []string          `json:"suggested_job_ids"`
	Available    []string          `json:"available_job_ids"`
	UnknownRules []string          `json:"unknown_rule_job_ids"`
	Blocked      map[string]string `json:"blocked"`
	Notice       string            `json:"notice"`
}

func BuildCompanyDecisionPlan(s MatchSnapshot, report matching.CompanyComparison) CompanyDecisionPlan {
	p := CompanyDecisionPlan{Suggested: []string{}, Available: []string{}, UnknownRules: []string{}, Blocked: map[string]string{}, Notice: "方案仅覆盖本次候选；未知规则需核对官网。相对排序解释适配，名额由后端校验。"}
	keys := []string{s.CandidateHash}
	for _, r := range s.Jobs {
		keys = append(keys, r.Job.ID+":"+r.InputKey)
	}
	sort.Strings(keys)
	p.Key = d.Hash(d.JSON(keys) + d.JSON(s.CompanyWorkflow))
	// as_of is a display clock, not a business dependency.
	if s.CompanyWorkflow != nil {
		copy := *s.CompanyWorkflow
		copy.AsOf = time.Time{}
		p.Key = d.Hash(d.JSON(keys) + d.JSON(copy))
	}
	byRule := map[string]CampaignView{}
	membership := map[string]string{}
	remaining := map[string]int{}
	if s.CompanyWorkflow != nil {
		for _, r := range s.CompanyWorkflow.Campaigns {
			byRule[r.ID] = r
			remaining[r.ID] = r.Remaining
			for _, id := range r.JobIDs {
				membership[id] = r.ID
			}
		}
	}
	rows := map[string]MatchJob{}
	statuses := map[string]string{}
	if s.CompanyWorkflow != nil {
		for _, c := range s.CompanyWorkflow.Jobs {
			statuses[c.JobID] = c.CurrentStatus
		}
	}
	for _, r := range s.Jobs {
		if status := statuses[r.Job.ID]; status != "" {
			r.Job.CurrentStatus = status
		}
		rows[r.Job.ID] = r
		reason := ""
		switch {
		case r.Application != nil:
			reason = "已有申请记录"
		case r.Job.CurrentStatus == "CLOSED":
			reason = "岗位已关闭"
		case r.ExcludedReason != "":
			reason = r.ExcludedReason
		case r.Result != nil && r.State == "ANALYZED" && r.Result.Qualifications.Status == "INELIGIBLE":
			reason = "当前分析存在明确资格障碍"
		}
		if rule, ok := byRule[membership[r.Job.ID]]; ok && rule.Remaining <= 0 {
			reason = "该批次名额已占用"
		}
		if reason != "" {
			p.Blocked[r.Job.ID] = reason
			continue
		}
		p.Available = append(p.Available, r.Job.ID)
		if membership[r.Job.ID] == "" {
			p.UnknownRules = append(p.UnknownRules, r.Job.ID)
		}
	}
	if report.Holistic == nil {
		p.Notice += " 当前范围没有有效整体比较，暂不自动指定首选。"
		return p
	}
	choices := append([]matching.CompanyChoice{}, report.Holistic.Choices...)
	sort.SliceStable(choices, func(i, j int) bool { return choices[i].Rank < choices[j].Rank })
	for _, c := range choices {
		r, ok := rows[c.ID]
		if !ok || p.Blocked[c.ID] != "" {
			continue
		}
		if r.Holistic != nil && r.Holistic.Fit == "WEAK" {
			continue
		}
		if rule := membership[c.ID]; rule != "" {
			if remaining[rule] <= 0 {
				continue
			}
			remaining[rule]--
		} else {
			continue
		} // Unknown rules never imply unlimited slots.
		p.Suggested = append(p.Suggested, c.ID)
	}
	return p
}
func (s *Store) ConfirmCompanyDecision(ctx context.Context, user, model, mask, company string, scope, selected []string, key string) ([]d.Application, error) {
	out := []d.Application{}
	if len(key) != 64 || len(selected) < 1 || len(selected) > 16 || len(scope) > 16 {
		return out, ErrValidation
	}
	sorted := append([]string{}, selected...)
	sort.Strings(sorted)
	nonce := d.Hash(user + ":company-plan:" + key + d.JSON(sorted))[:32]
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		if e := lockRunUser(ctx, tx, user); e != nil {
			return e
		}
		old, e := One[[]d.Application](ctx, tx, "SELECT result FROM action_receipts WHERE id=? AND user_id=?", nonce, user)
		if e == nil {
			out = old
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		snapshot, e := s.matchSnapshotTx(ctx, tx, user, model, mask, scope, true, company, true, true)
		if e != nil {
			return e
		}
		// Ingest locks the same parents. Stable ordering prevents opposite selection
		// orders from creating a lock cycle, and the second read fences changed JDs.
		lockedJobs := []string{}
		for _, row := range snapshot.Jobs {
			lockedJobs = append(lockedJobs, row.Job.ID)
		}
		sort.Strings(lockedJobs)
		for _, job := range lockedJobs {
			var locked string
			if e = tx.QueryRowContext(ctx, "SELECT id FROM jobs WHERE id=? FOR UPDATE", job).Scan(&locked); e != nil {
				return e
			}
		}
		snapshot, e = s.matchSnapshotTx(ctx, tx, user, model, mask, scope, true, company, true, true)
		if e != nil {
			return e
		}
		p := BuildCompanyDecisionPlan(snapshot, matching.CompanyComparison{})
		if key != p.Key {
			return ErrConflict
		}
		available := map[string]bool{}
		for _, id := range p.Available {
			available[id] = true
		}
		seen := map[string]bool{}
		for _, id := range sorted {
			if len(id) != 32 || !available[id] || seen[id] {
				return ErrValidation
			}
			seen[id] = true
			if e = checkCampaignPlan(ctx, tx, user, id); e != nil {
				return e
			}
			// A confirmed batch action must be bound to a known user-confirmed rule.
			var bound int
			if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM application_campaign_jobs WHERE user_id=? AND job_id=?", user, id).Scan(&bound); e != nil {
				return e
			}
			if bound == 0 {
				return ErrValidation
			}
			now := time.Now().UTC()
			a := d.Application{ID: d.ID(), UserID: user, JobID: id, State: "PLANNED", Version: 1, CreatedAt: now, UpdatedAt: now}
			if _, e = tx.ExecContext(ctx, "INSERT INTO applications(id,user_id,job_id,version,body) VALUES(?,?,?,?,?)", a.ID, user, id, a.Version, d.JSON(a)); e != nil {
				return e
			}
			if e = saveApplicationContext(ctx, tx, user, a, "PLANNED", "", ""); e != nil {
				return e
			}
			event := d.ApplicationEvent{ID: d.ID(), ApplicationID: a.ID, To: a.State, OccurredAt: now, Actor: user, Note: "确认公司内受约束选岗方案"}
			if _, e = tx.ExecContext(ctx, "INSERT INTO application_events(id,application_id,body) VALUES(?,?,?)", event.ID, a.ID, d.JSON(event)); e != nil {
				return e
			}
			out = append(out, a)
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO action_receipts(id,user_id,result,created_at) VALUES(?,?,?,?)", nonce, user, d.JSON(out), time.Now().UTC())
		return e
	})
	return out, err
}

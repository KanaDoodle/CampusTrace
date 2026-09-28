package persistence

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	"github.com/KanaDoodle/CampusTrace/internal/resume"
)

var ErrMatchQuota = errors.New("daily matching call limit reached")

type MatchJob struct {
	Job              d.Job    `json:"job"`
	Text             string   `json:"-"`
	TextBytes        int      `json:"text_bytes"`
	RequirementsKey  string   `json:"requirements_key"`
	InputKey         string   `json:"input_key"`
	PreliminaryScore float64  `json:"preliminary_score"`
	ExcludedReason   string   `json:"excluded_reason"`
	State            string   `json:"state"`
	Score            *float64 `json:"score"`
	Coverage         float64  `json:"coverage"`
	Disposition      string   `json:"disposition"`
}
type MatchSnapshot struct {
	Profile       d.Profile          `json:"-"`
	Candidate     matching.Candidate `json:"candidate"`
	CandidateHash string             `json:"candidate_hash"`
	Jobs          []MatchJob         `json:"jobs"`
	Settings      matching.Settings  `json:"settings"`
	CallsToday    int                `json:"calls_today"`
}

func matchDay(now time.Time) string {
	return now.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02")
}

func matchSettings(ctx context.Context, q Queryer, user string) (matching.Settings, error) {
	v, err := One[matching.Settings](ctx, q, "SELECT body FROM match_settings WHERE user_id=?", user)
	if errors.Is(err, sql.ErrNoRows) {
		return matching.Defaults(), nil
	}
	return v, err
}
func (s *Store) SaveMatchSettings(ctx context.Context, user string, v matching.Settings) error {
	if err := v.Validate(); err != nil {
		return ErrValidation
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var id string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO match_settings(user_id,body) VALUES(?,?) ON DUPLICATE KEY UPDATE body=VALUES(body)", user, d.JSON(v))
		return err
	})
}
func (s *Store) ReserveMatchCall(ctx context.Context, user string, now time.Time) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var id string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&id); err != nil {
			return err
		}
		settings, err := matchSettings(ctx, tx, user)
		if err != nil {
			return err
		}
		day := matchDay(now)
		if _, err = tx.ExecContext(ctx, "INSERT IGNORE INTO match_daily_usage(user_id,usage_day,calls) VALUES(?,?,0)", user, day); err != nil {
			return err
		}
		var calls int
		if err = tx.QueryRowContext(ctx, "SELECT calls FROM match_daily_usage WHERE user_id=? AND usage_day=? FOR UPDATE", user, day).Scan(&calls); err != nil {
			return err
		}
		if calls >= settings.DailyCalls {
			return ErrMatchQuota
		}
		_, err = tx.ExecContext(ctx, "UPDATE match_daily_usage SET calls=calls+1 WHERE user_id=? AND usage_day=?", user, day)
		return err
	})
}
func matchCandidate(ctx context.Context, q Queryer, user, maskName string) (d.Profile, matching.Candidate, error) {
	p, err := One[d.Profile](ctx, q, "SELECT body FROM profiles WHERE user_id=?", user)
	if err != nil {
		return p, matching.Candidate{}, err
	}
	facts, err := Many[d.ProjectFact](ctx, q, "SELECT body FROM project_facts WHERE user_id=? ORDER BY id", user)
	if err != nil {
		return p, matching.Candidate{}, err
	}
	c, err := matching.CandidateFrom(p, facts, maskName)
	return p, c, err
}
func cleanJobText(text, maskName string) string {
	if maskName != "" {
		text = strings.ReplaceAll(text, maskName, "[已遮盖姓名]")
	}
	return resume.Redact(text)
}

// This catalog does not use Radar's 500-job cap or the search screen's 100-row
// limit. All accessible jobs (up to an explicit 10,000 cap) get a local score.
func (s *Store) MatchSnapshot(ctx context.Context, user, model, maskName string, ids []string) (MatchSnapshot, error) {
	v := MatchSnapshot{Jobs: []MatchJob{}}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return v, err
	}
	defer tx.Rollback()
	v.Profile, v.Candidate, err = matchCandidate(ctx, tx, user, maskName)
	if err != nil {
		return v, err
	}
	v.CandidateHash = v.Candidate.Hash()
	v.Settings, err = matchSettings(ctx, tx, user)
	if err != nil {
		return v, err
	}
	err = tx.QueryRowContext(ctx, "SELECT calls FROM match_daily_usage WHERE user_id=? AND usage_day=?", user, matchDay(time.Now())).Scan(&v.CallsToday)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return v, err
	}
	query := "SELECT body FROM jobs WHERE (visibility='GLOBAL' OR (visibility='PRIVATE' AND owner_id=?))"
	args := []any{user}
	if len(ids) > 0 {
		slots := []string{}
		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] || len(id) != 32 {
				return v, ErrValidation
			}
			seen[id] = true
			slots = append(slots, "?")
			args = append(args, id)
		}
		query += " AND id IN (" + strings.Join(slots, ",") + ")"
	}
	query += " ORDER BY id LIMIT 10001"
	jobs, err := Many[d.Job](ctx, tx, query, args...)
	if err != nil {
		return v, err
	}
	if len(jobs) > 10000 {
		return v, matching.ErrCapacity
	}
	if len(ids) > 0 && len(jobs) != len(ids) {
		return v, ErrNotFound
	}
	results, err := Many[matching.Result](ctx, tx, "SELECT JSON_OBJECT('job_id',job_id,'input_key',JSON_UNQUOTE(JSON_EXTRACT(body,'$.input_key')),'model',JSON_UNQUOTE(JSON_EXTRACT(body,'$.model')),'score',JSON_EXTRACT(body,'$.score'),'coverage',JSON_EXTRACT(body,'$.coverage')) FROM job_match_results WHERE user_id=?", user)
	if err != nil {
		return v, err
	}
	resultByID := map[string]matching.Result{}
	for _, r := range results {
		resultByID[r.JobID] = r
	}
	prefs, err := Many[d.UserJobPreference](ctx, tx, "SELECT JSON_OBJECT('job_id',job_id,'disposition',disposition) FROM user_job_preferences WHERE user_id=?", user)
	if err != nil {
		return v, err
	}
	prefByID := map[string]string{}
	for _, p := range prefs {
		prefByID[p.JobID] = p.Disposition
	}
	latest := map[string]d.Observation{}
	for start := 0; start < len(jobs); start += 250 {
		end := start + 250
		if end > len(jobs) {
			end = len(jobs)
		}
		placeholders := []string{}
		values := []any{}
		for _, j := range jobs[start:end] {
			placeholders = append(placeholders, "?")
			values = append(values, j.ID)
		}
		observations, err := Many[d.Observation](ctx, tx, "SELECT o.body FROM observations o WHERE o.job_id IN ("+strings.Join(placeholders, ",")+") AND NOT EXISTS (SELECT 1 FROM observations newer WHERE newer.job_id=o.job_id AND (newer.observed_at>o.observed_at OR (newer.observed_at=o.observed_at AND newer.id>o.id)))", values...)
		if err != nil {
			return v, err
		}
		for _, o := range observations {
			latest[o.JobID] = o
		}
	}
	for _, job := range jobs {
		row := MatchJob{Job: job, State: "BASIC", Disposition: prefByID[job.ID]}
		o, exists := latest[job.ID]
		if exists && o.FetchStatus == "SUCCESS" {
			row.Text = cleanJobText(o.Text, maskName)
			row.TextBytes = len(row.Text)
			row.RequirementsKey = matching.RequirementKey(row.Text, model)
			row.InputKey = matching.InputKey(row.RequirementsKey, v.CandidateHash)
			row.PreliminaryScore, row.ExcludedReason = matching.Preliminary(job, v.Profile, o.Text, time.Now().UTC())
		} else {
			row.ExcludedReason = "最近一次未取得可用岗位原文"
		}
		if job.CurrentStatus == "CLOSED" {
			row.ExcludedReason = "岗位已关闭"
		}
		if row.Disposition == "IGNORED" {
			row.ExcludedReason = "你已忽略这个岗位"
		}
		if r, ok := resultByID[job.ID]; ok {
			row.State = "STALE"
			if r.InputKey == row.InputKey && r.Model == model {
				row.State = "ANALYZED"
				row.Score = r.Score
				row.Coverage = r.Coverage
			}
		}
		v.Jobs = append(v.Jobs, row)
	}
	sort.SliceStable(v.Jobs, func(i, j int) bool {
		a, b := v.Jobs[i], v.Jobs[j]
		if (a.ExcludedReason == "") != (b.ExcludedReason == "") {
			return a.ExcludedReason == ""
		}
		if a.PreliminaryScore != b.PreliminaryScore {
			return a.PreliminaryScore > b.PreliminaryScore
		}
		return a.Job.ID < b.Job.ID
	})
	return v, tx.Commit()
}
func (s *Store) CachedRequirements(ctx context.Context, user, key string) (matching.Requirements, error) {
	return One[matching.Requirements](ctx, s.DB, "SELECT body FROM job_requirement_cache WHERE user_id=? AND content_key=?", user, key)
}
func (s *Store) SaveRequirements(ctx context.Context, user, key string, v matching.Requirements) error {
	_, err := s.DB.ExecContext(ctx, "INSERT INTO job_requirement_cache(user_id,content_key,body) VALUES(?,?,?) ON DUPLICATE KEY UPDATE body=VALUES(body)", user, key, d.JSON(v))
	return err
}
func (s *Store) MatchResult(ctx context.Context, user, job string) (matching.Result, error) {
	if err := CheckJobAccess(ctx, s.DB, user, job); err != nil {
		return matching.Result{}, err
	}
	return One[matching.Result](ctx, s.DB, "SELECT body FROM job_match_results WHERE user_id=? AND job_id=?", user, job)
}
func (s *Store) SaveMatchResult(ctx context.Context, user, maskName string, r matching.Result) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var id string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&id); err != nil {
			return err
		}
		if err := CheckJobAccess(ctx, tx, user, r.JobID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, "SELECT id FROM jobs WHERE id=? FOR UPDATE", r.JobID).Scan(&id); err != nil {
			return err
		}
		_, candidate, err := matchCandidate(ctx, tx, user, maskName)
		if err != nil {
			return err
		}
		o, err := One[d.Observation](ctx, tx, "SELECT body FROM observations WHERE job_id=? ORDER BY observed_at DESC,id DESC LIMIT 1", r.JobID)
		if err != nil {
			return err
		}
		key := matching.RequirementKey(cleanJobText(o.Text, maskName), r.Model)
		if o.FetchStatus != "SUCCESS" || matching.InputKey(key, candidate.Hash()) != r.InputKey {
			return ErrStaleInput
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO job_match_results(user_id,job_id,body) VALUES(?,?,?) ON DUPLICATE KEY UPDATE body=VALUES(body)", user, r.JobID, d.JSON(r))
		return err
	})
}

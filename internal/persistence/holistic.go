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
)

func WholeJobs(rows []MatchJob, masks ...string) []matching.HolisticJob {
	mask := ""
	if len(masks) > 0 {
		mask = masks[0]
	}
	out := make([]matching.HolisticJob, 0, len(rows))
	for _, r := range rows {
		out = append(out, matching.HolisticJob{ID: r.Job.ID, InputKey: r.InputKey, Company: cleanJobText(r.Job.Company, mask), Title: cleanJobText(r.Job.Title, mask), Locations: cleanMetadataCities(r.Cities, mask), JobType: r.Job.JobType, Text: r.Text})
	}
	return out
}
func (s *Store) CompanyReport(ctx context.Context, user, company, key string) (matching.HolisticCompanyReport, error) {
	return One[matching.HolisticCompanyReport](ctx, s.DB, "SELECT body FROM company_match_reports WHERE user_id=? AND company_name=? AND (input_key=? OR JSON_UNQUOTE(JSON_EXTRACT(body,'$.source_context_key'))=?) ORDER BY updated_at DESC LIMIT 1", user, company, key, key)
}

func companyChatVersion(ctx context.Context, q Queryer, user, company, key string) (string, error) {
	r, err := One[matching.HolisticCompanyReport](ctx, q, "SELECT body FROM company_match_reports WHERE user_id=? AND company_name=? AND (input_key=? OR JSON_UNQUOTE(JSON_EXTRACT(body,'$.source_context_key'))=?) ORDER BY updated_at DESC LIMIT 1", user, company, key, key)
	if errors.Is(err, ErrNotFound) {
		return "NONE", nil
	}
	if err != nil {
		return "", err
	}
	return d.Hash(d.JSON(r)), nil
}

func (s *Store) CompanyChatVersion(ctx context.Context, user, company, key string) (string, error) {
	return companyChatVersion(ctx, s.DB, user, company, key)
}

// Preview and commit share the same user lock and source fences. Importing a
// company comparison never replaces individual job analyses or human labels.
func (s *Store) SaveReviewedCompanyReport(ctx context.Context, user, mask string, r matching.HolisticCompanyReport, jobs []matching.HolisticJob, previous string) error {
	if r.Model != matching.ChatIdentity || previous == "" {
		return ErrValidation
	}
	return s.Tx(ctx, func(tx *sql.Tx) error { return s.saveCompanyReport(ctx, tx, user, mask, r, jobs, previous) })
}

// The same user lock and execution guard fence both report scopes. Re-read every
// source at commit time; a rank never survives a changed candidate or JD set.
func (s *Store) SaveCompanyReport(ctx context.Context, user, mask string, r matching.HolisticCompanyReport, jobs []matching.HolisticJob) error {
	return s.Tx(ctx, func(tx *sql.Tx) error { return s.saveCompanyReport(ctx, tx, user, mask, r, jobs) })
}
func (s *Store) saveCompanyReport(ctx context.Context, tx *sql.Tx, user, mask string, r matching.HolisticCompanyReport, jobs []matching.HolisticJob, reviewed ...string) error {
	if err := lockRunUser(ctx, tx, user); err != nil {
		return err
	}
	if err := checkMatchRunGuard(ctx, tx, user, "", ""); err != nil {
		return err
	}
	_, candidate, err := matchCandidate(ctx, tx, user, mask)
	if err != nil {
		return err
	}
	ordered := append([]matching.HolisticJob{}, jobs...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	current := make([]matching.HolisticJob, 0, len(jobs))
	for _, j := range ordered {
		if err := checkMatchRunGuard(ctx, tx, user, j.ID, j.InputKey); err != nil {
			return err
		}
		if err := CheckJobAccess(ctx, tx, user, j.ID); err != nil {
			return err
		}
		job, err := One[d.Job](ctx, tx, "SELECT body FROM jobs WHERE id=? FOR UPDATE", j.ID)
		if err != nil {
			return err
		}
		o, err := One[d.Observation](ctx, tx, "SELECT body FROM observations WHERE job_id=? ORDER BY observed_at DESC,id DESC LIMIT 1 FOR UPDATE", j.ID)
		if err != nil {
			return err
		}
		if o.FetchStatus != "SUCCESS" || job.Company != r.Company || job.CurrentStatus == "CLOSED" {
			return ErrStaleInput
		}
		var disposition string
		e := tx.QueryRowContext(ctx, "SELECT disposition FROM user_job_preferences WHERE user_id=? AND job_id=?", user, j.ID).Scan(&disposition)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if disposition == "IGNORED" {
			return ErrStaleInput
		}
		text := cleanJobText(o.Text, mask)
		key := matching.JobInputKey(job, matching.RequirementKey(text, r.Model), candidate.Hash())
		if key != j.InputKey {
			return ErrStaleInput
		}
		current = append(current, WholeJobs([]MatchJob{{Job: job, InputKey: key, Cities: d.CanonicalCities(job.Locations), Text: text}}, mask)...)
	}
	if r.CandidateHash != candidate.Hash() || r.InputKey != matching.CompanyInputKey(candidate, current, r.Model) {
		return ErrStaleInput
	}
	if err := matching.ValidateHolisticInput(candidate, current, true); err != nil {
		return err
	}
	validated := r
	validated.Company = current[0].Company
	if err := matching.ValidateCompanyReport(validated, candidate, current); err != nil {
		return err
	}
	// Manual redaction is ephemeral. Retain only a hash of the current local
	// unmasked sources so the accepted report remains readable after reload.
	r.SourceContextKey = ""
	if r.Model == matching.ChatIdentity {
		_, base, e := matchCandidate(ctx, tx, user, "")
		if e != nil {
			return e
		}
		baseJobs := []matching.HolisticJob{}
		for _, j := range jobs {
			job, e := One[d.Job](ctx, tx, "SELECT body FROM jobs WHERE id=?", j.ID)
			if e != nil {
				return e
			}
			o, e := One[d.Observation](ctx, tx, "SELECT body FROM observations WHERE job_id=? ORDER BY observed_at DESC,id DESC LIMIT 1", j.ID)
			if e != nil {
				return e
			}
			text := cleanJobText(o.Text, "")
			key := matching.JobInputKey(job, matching.RequirementKey(text, matching.ChatIdentity), base.Hash())
			baseJobs = append(baseJobs, WholeJobs([]MatchJob{{Job: job, InputKey: key, Cities: d.CanonicalCities(job.Locations), Text: text}})...)
		}
		r.SourceContextKey = matching.CompanyInputKey(base, baseJobs, matching.ChatIdentity)
	}
	if len(reviewed) > 0 {
		previous, e := companyChatVersion(ctx, tx, user, r.Company, r.SourceContextKey)
		if e != nil {
			return e
		}
		if previous != reviewed[0] {
			return ErrStaleInput
		}
	}
	r.AnalyzedAt = time.Now().UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, "INSERT INTO company_match_reports(user_id,company_name,input_key,body,updated_at) VALUES(?,?,?,?,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE body=VALUES(body),updated_at=VALUES(updated_at)", user, strings.TrimSpace(r.Company), r.InputKey, d.JSON(r))
	return err
}

func (s *Store) AttachCompanyReport(ctx context.Context, user string, snapshot MatchSnapshot, company, identity string, report *matching.CompanyComparison, masks ...string) error {
	rows := []MatchJob{}
	for _, row := range snapshot.Jobs {
		if row.ExcludedReason == "" {
			rows = append(rows, row)
		}
	}
	jobs := WholeJobs(rows, masks...)
	report.HolisticJobIDs = []string{}
	for _, job := range jobs {
		report.HolisticJobIDs = append(report.HolisticJobIDs, job.ID)
	}
	if len(jobs) > 0 {
		report.HolisticInputKey = matching.CompanyInputKey(snapshot.Candidate, jobs, identity)
		if err := matching.ValidateHolisticInput(snapshot.Candidate, jobs, true); err != nil {
			report.HolisticNotice = "整体比较每次最多16个岗位，并按完整文字量限制；请通过筛选或勾选缩小候选范围。"
		} else {
			chatJobs := append([]matching.HolisticJob{}, jobs...)
			for i := range chatJobs {
				chatJobs[i].InputKey = matching.JobInputKey(rows[i].Job, matching.RequirementKey(chatJobs[i].Text, matching.ChatIdentity), snapshot.CandidateHash)
			}
			chatKey := matching.CompanyInputKey(snapshot.Candidate, chatJobs, matching.ChatIdentity)
			// Prefer the newest accepted report across API and manual chat sources.
			// Otherwise a successful import remains hidden behind an older API run.
			cached, e := One[matching.HolisticCompanyReport](ctx, s.DB, "SELECT body FROM company_match_reports WHERE user_id=? AND company_name=? AND (input_key IN (?,?) OR JSON_UNQUOTE(JSON_EXTRACT(body,'$.source_context_key')) IN (?,?)) ORDER BY updated_at DESC LIMIT 1", user, company, report.HolisticInputKey, chatKey, report.HolisticInputKey, chatKey)
			if e == nil {
				report.Holistic = &cached
			} else if !errors.Is(e, ErrNotFound) {
				return e
			}
		}
	} else {
		report.HolisticNotice = "此范围暂时没有可分析的岗位原文。"
	}
	return nil
}

func cleanMetadataCities(values []string, mask string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, cleanJobText(value, mask))
	}
	return out
}

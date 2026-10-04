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

// Inventory previews need cache identity and scores, not every citation and
// historical candidate fact. Keep this smaller than matching.Result.
type matchResultSummary struct {
	JobID, InputKey, Model, RequirementsKey, ComparisonKey, ComparisonScope string
	Score                                                                   *float64
	Coverage                                                                float64
}

type MatchJob struct {
	Job              d.Job                 `json:"job"`
	Text             string                `json:"-"`
	TextBytes        int                   `json:"text_bytes"`
	RequirementsKey  string                `json:"requirements_key"`
	InputKey         string                `json:"input_key"`
	PreliminaryScore float64               `json:"preliminary_score"`
	Local            *matching.LocalScreen `json:"local,omitempty"`
	ExcludedReason   string                `json:"excluded_reason"`
	State            string                `json:"state"`
	Score            *float64              `json:"score"`
	Coverage         float64               `json:"coverage"`
	Disposition      string                `json:"disposition"`
	Application      *MatchApplication     `json:"application,omitempty"`
	Result           *matching.Result      `json:"-"`
}

// The inventory needs workflow context, never application notes or resume names.
type MatchApplication struct {
	ID        string     `json:"id"`
	JobID     string     `json:"job_id"`
	State     string     `json:"current_state"`
	Version   int        `json:"version"`
	AppliedAt *time.Time `json:"applied_at,omitempty"`
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
	projects, err := Many[d.Project](ctx, q, "SELECT body FROM projects WHERE user_id=? ORDER BY id", user)
	if err != nil {
		return p, matching.Candidate{}, err
	}
	c, err := matching.CandidateWithProjects(p, facts, projects, maskName)
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
	return s.matchSnapshot(ctx, user, model, maskName, ids, true, "", false)
}

// Manual export needs the current texts and candidate, without running the
// preliminary rules again over potentially megabytes of selected descriptions.
func (s *Store) MatchExportSnapshot(ctx context.Context, user, model, maskName string, ids []string) (MatchSnapshot, error) {
	return s.matchSnapshot(ctx, user, model, maskName, ids, false, "", false)
}

// Decision views bind full results, current texts and personal facts to one
// read-only snapshot. No model call or durable change is made here.
func (s *Store) MatchDecisionSnapshot(ctx context.Context, user, model, maskName string, ids []string, company string) (MatchSnapshot, error) {
	if len(ids) > matching.MaxDecisionJobs || (company == "" && len(ids) == 0) {
		return MatchSnapshot{}, ErrValidation
	}
	return s.matchSnapshot(ctx, user, model, maskName, ids, true, company, true)
}

func (s *Store) matchSnapshot(ctx context.Context, user, model, maskName string, ids []string, screen bool, company string, fullResults bool) (MatchSnapshot, error) {
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
	var screener *matching.LocalScreener
	if screen {
		screener = matching.NewLocalScreener(v.Profile, v.Candidate)
	}
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
	if company != "" {
		query += " AND company_name=?"
		args = append(args, company)
	}
	if fullResults {
		query += " ORDER BY id LIMIT 201"
	} else {
		query += " ORDER BY id LIMIT 10001"
	}
	jobs, err := Many[d.Job](ctx, tx, query, args...)
	if err != nil {
		return v, err
	}
	if fullResults && len(jobs) > matching.MaxDecisionJobs {
		return v, matching.ErrDecisionCapacity
	}
	if len(jobs) > 10000 {
		return v, matching.ErrCapacity
	}
	if len(ids) > 0 && len(jobs) != len(ids) {
		return v, ErrNotFound
	}
	resultQuery := "SELECT JSON_OBJECT('JobID',job_id,'InputKey',JSON_UNQUOTE(JSON_EXTRACT(body,'$.input_key')),'Model',JSON_UNQUOTE(JSON_EXTRACT(body,'$.model')),'RequirementsKey',JSON_UNQUOTE(JSON_EXTRACT(body,'$.requirements_key')),'ComparisonKey',JSON_UNQUOTE(JSON_EXTRACT(body,'$.comparison_key')),'ComparisonScope',JSON_UNQUOTE(JSON_EXTRACT(body,'$.comparison_scope')),'Score',JSON_EXTRACT(body,'$.score'),'Coverage',JSON_EXTRACT(body,'$.coverage')) FROM job_match_results WHERE user_id=?"
	resultArgs := []any{user}
	if fullResults || len(ids) > 0 || company != "" {
		if fullResults {
			resultQuery = "SELECT body FROM job_match_results WHERE user_id=?"
		}
		resultQuery += " AND job_id IN ("
		slots := []string{}
		for _, j := range jobs {
			slots = append(slots, "?")
			resultArgs = append(resultArgs, j.ID)
		}
		if len(slots) == 0 {
			resultQuery = "SELECT body FROM job_match_results WHERE user_id=? AND 1=0"
		} else {
			resultQuery += strings.Join(slots, ",") + ")"
		}
	}
	resultByID := map[string]matchResultSummary{}
	fullByID := map[string]matching.Result{}
	if fullResults {
		results, e := Many[matching.Result](ctx, tx, resultQuery, resultArgs...)
		if e != nil {
			return v, e
		}
		for _, r := range results {
			fullByID[r.JobID] = r
			resultByID[r.JobID] = matchResultSummary{r.JobID, r.InputKey, r.Model, r.RequirementsKey, r.ComparisonKey, r.ComparisonScope, r.Score, r.Coverage}
		}
	} else {
		results, e := Many[matchResultSummary](ctx, tx, resultQuery, resultArgs...)
		if e != nil {
			return v, e
		}
		resultByID = make(map[string]matchResultSummary, len(results))
		for _, r := range results {
			resultByID[r.JobID] = r
		}
	}
	prefQuery := "SELECT JSON_OBJECT('job_id',job_id,'disposition',disposition) FROM user_job_preferences WHERE user_id=?"
	prefArgs := []any{user}
	if len(ids) > 0 || company != "" {
		slots := []string{}
		for _, j := range jobs {
			slots = append(slots, "?")
			prefArgs = append(prefArgs, j.ID)
		}
		if len(slots) == 0 {
			prefQuery += " AND 1=0"
		} else {
			prefQuery += " AND job_id IN (" + strings.Join(slots, ",") + ")"
		}
	}
	prefs, err := Many[d.UserJobPreference](ctx, tx, prefQuery, prefArgs...)
	if err != nil {
		return v, err
	}
	prefByID := map[string]string{}
	for _, p := range prefs {
		prefByID[p.JobID] = p.Disposition
	}
	applications, err := Many[MatchApplication](ctx, tx, `SELECT JSON_OBJECT('id',a.id,'job_id',a.job_id,
 'current_state',JSON_EXTRACT(a.body,'$.current_state'),'version',a.version,
 'applied_at',JSON_EXTRACT(a.body,'$.applied_at')) FROM applications a JOIN jobs j ON j.id=a.job_id
 WHERE a.user_id=? AND (j.visibility='GLOBAL' OR (j.visibility='PRIVATE' AND j.owner_id=?))`, user, user)
	if err != nil {
		return v, err
	}
	applicationByID := make(map[string]*MatchApplication, len(applications))
	for i := range applications {
		applicationByID[applications[i].JobID] = &applications[i]
	}
	// Read only the fields required by this snapshot. The latest FAILED row
	// still wins; never fall back to an older successful text.
	type matchObservation struct{ Text, FetchStatus string }
	latest := make(map[string]matchObservation, len(jobs))
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
		rows, err := tx.QueryContext(ctx, "SELECT o.job_id,JSON_UNQUOTE(JSON_EXTRACT(o.body,'$.text')),JSON_UNQUOTE(JSON_EXTRACT(o.body,'$.fetch_status')) FROM observations o WHERE o.job_id IN ("+strings.Join(placeholders, ",")+") AND NOT EXISTS (SELECT 1 FROM observations newer WHERE newer.job_id=o.job_id AND (newer.observed_at>o.observed_at OR (newer.observed_at=o.observed_at AND newer.id>o.id)))", values...)
		if err != nil {
			return v, err
		}
		for rows.Next() {
			var id string
			var text, status sql.NullString
			if err := rows.Scan(&id, &text, &status); err != nil {
				rows.Close()
				return v, err
			}
			latest[id] = matchObservation{text.String, status.String}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return v, err
		}
		rows.Close()
	}
	v.Jobs = make([]MatchJob, 0, len(jobs))
	hashes := map[string]string{matching.ComparisonAbilities: matching.ComparisonCandidateHash(v.Candidate, matching.ComparisonAbilities), matching.ComparisonFull: matching.ComparisonCandidateHash(v.Candidate, matching.ComparisonFull)}
	// This bounded memo exists only inside the current user's read snapshot.
	// Repeated descriptions share redaction/hashing, never cross-user raw text.
	type cleanInput struct{ Text, RequirementsKey, InputKey string }
	cleaned := map[string]cleanInput{}
	memoBytes := 0
	for _, job := range jobs {
		row := MatchJob{Job: job, State: "BASIC", Disposition: prefByID[job.ID], Application: applicationByID[job.ID]}
		o, exists := latest[job.ID]
		if exists && o.FetchStatus == "SUCCESS" {
			input, hit := cleaned[o.Text]
			if !hit {
				input.Text = cleanJobText(o.Text, maskName)
				input.RequirementsKey = matching.RequirementKey(input.Text, model)
				input.InputKey = matching.InputKey(input.RequirementsKey, v.CandidateHash)
				if len(cleaned) < 256 && memoBytes+len(o.Text)+len(input.Text) <= 8<<20 {
					cleaned[o.Text] = input
					memoBytes += len(o.Text) + len(input.Text)
				}
			}
			row.Text = input.Text
			row.TextBytes = len(row.Text)
			row.RequirementsKey, row.InputKey = input.RequirementsKey, input.InputKey
			if screen {
				local := screener.Screen(job, row.Text)
				row.Local = &local
				row.PreliminaryScore, row.ExcludedReason = local.Score, local.ExcludedReason
			}
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
			if fullResults {
				copy := fullByID[job.ID]
				row.Result = &copy
			}
			row.State = "STALE"
			identity := matching.Result{RequirementsKey: r.RequirementsKey, ComparisonKey: r.ComparisonKey, ComparisonScope: r.ComparisonScope}
			reusable := identity.CanReuse(row.RequirementsKey, hashes)
			legacyCurrent := r.ComparisonKey == "" && r.InputKey == row.InputKey
			if row.InputKey != "" && r.Model == model && (legacyCurrent || reusable) {
				row.State = "ANALYZED"
				row.Score = r.Score
				row.Coverage = r.Coverage
				if fullResults && reusable {
					refreshed, e := matching.RefreshResult(*row.Result, job, v.Profile, v.Candidate, row.InputKey, v.CandidateHash, time.Now().UTC())
					if e != nil {
						row.State, row.Score, row.Coverage = "STALE", nil, 0
					} else {
						row.Result = &refreshed
					}
				}
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
		if !a.Job.UpdatedAt.Equal(b.Job.UpdatedAt) {
			return a.Job.UpdatedAt.After(b.Job.UpdatedAt)
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
		if err := checkMatchRunGuard(ctx, tx, user, r.JobID, r.InputKey); err != nil {
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
		if r.ComparisonKey != "" {
			scope := matching.ComparisonScope(r.Requirements)
			if r.RequirementsKey != key || r.CandidateHash != candidate.Hash() || r.ComparisonScope != scope || r.ComparisonKey != matching.ComparisonKey(key, matching.ComparisonCandidateHash(candidate, scope), scope) {
				return ErrValidation
			}
			if err := matching.ValidateMatches(candidate, r.Requirements, r.Matches); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO job_match_results(user_id,job_id,body) VALUES(?,?,?) ON DUPLICATE KEY UPDATE body=VALUES(body)", user, r.JobID, d.JSON(r))
		return err
	})
}

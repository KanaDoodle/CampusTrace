package persistence

import (
	"context"
	"database/sql"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"regexp"
	"strings"
	"time"
)

var ErrMatchRunBusy = errors.New("another matching task is active")
var ErrMatchRunLease = errors.New("matching task execution ownership lost")
var ErrMatchJobUnavailable = errors.New("matching job unavailable")

type MatchRunItem struct {
	JobID      string          `json:"job_id"`
	InputKey   string          `json:"input_key"`
	State      string          `json:"state"`
	Stage      string          `json:"stage,omitempty"`
	Code       string          `json:"code,omitempty"`
	Diagnostic map[string]any  `json:"diagnostic,omitempty"`
	Reviews    int             `json:"evidence_reviews,omitempty"`
	Job        *ApplicationJob `json:"job,omitempty"`
}
type MatchRun struct {
	Kind               string         `json:"kind,omitempty"`
	Company            string         `json:"company,omitempty"`
	ScopeKey           string         `json:"scope_key,omitempty"`
	ID                 string         `json:"id"`
	State              string         `json:"state"`
	Version            int            `json:"version"`
	RequestKey         string         `json:"request_key"`
	RequestHash        string         `json:"request_hash"`
	CandidateHash      string         `json:"candidate_hash"`
	Model              string         `json:"model"`
	Items              []MatchRunItem `json:"items"`
	Calls              int            `json:"calls"`
	RequirementsReused int            `json:"requirements_reused"`
	RequestID          string         `json:"request_id"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
}
type MatchRunEvent struct {
	ID         string    `json:"id"`
	RequestID  string    `json:"request_id"`
	RunID      string    `json:"run_id"`
	Stage      string    `json:"stage"`
	JobIDs     []string  `json:"job_ids,omitempty"`
	Code       string    `json:"code,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}
type MatchRunGuard struct{ ID, Token string }
type matchRunGuardKey struct{}

func WithMatchRunGuard(ctx context.Context, g MatchRunGuard) context.Context {
	return context.WithValue(ctx, matchRunGuardKey{}, g)
}
func checkMatchRunGuard(ctx context.Context, q Queryer, user, job, input string) error {
	g, ok := ctx.Value(matchRunGuardKey{}).(MatchRunGuard)
	if !ok {
		return nil
	}
	var body []byte
	if err := q.QueryRowContext(ctx, "SELECT body FROM match_runs WHERE id=? AND user_id=? AND token=? AND state IN ('RUNNING','PAUSING') AND lease_until>UTC_TIMESTAMP(6) FOR UPDATE", g.ID, user, g.Token).Scan(&body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrMatchRunLease
		}
		return err
	}
	if job != "" {
		var run MatchRun
		if err := d.Strict(body, &run); err != nil {
			return err
		}
		for _, item := range run.Items {
			if item.JobID == job && item.InputKey == input && item.State == "RUNNING" {
				return nil
			}
		}
		return ErrMatchRunLease
	}
	return nil
}
func (s *Store) CheckMatchRunGuard(ctx context.Context, user string) error {
	if _, ok := ctx.Value(matchRunGuardKey{}).(MatchRunGuard); !ok {
		return nil
	}
	return s.Tx(ctx, func(tx *sql.Tx) error { return checkMatchRunGuard(ctx, tx, user, "", "") })
}
func lockRunUser(ctx context.Context, tx *sql.Tx, user string) error {
	var id string
	return tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&id)
}
func writeRun(ctx context.Context, tx *sql.Tx, user string, v *MatchRun) error {
	v.Version++
	v.UpdatedAt = time.Now().UTC()
	_, err := tx.ExecContext(ctx, "UPDATE match_runs SET state=?,updated_at=?,body=? WHERE id=? AND user_id=?", v.State, v.UpdatedAt, d.JSON(v), v.ID, user)
	return err
}

// Expired execution is never silently retried: a paid provider may have
// completed a request even when our process could not save its response.
func recoverRuns(ctx context.Context, tx *sql.Tx, user string) error {
	runs, err := Many[MatchRun](ctx, tx, "SELECT body FROM match_runs WHERE user_id=? AND state IN ('RUNNING','PAUSING') AND lease_until<=UTC_TIMESTAMP(6) FOR UPDATE", user)
	if err != nil {
		return err
	}
	for _, v := range runs {
		v.State = "WAITING_AUTH"
		for i := range v.Items {
			if v.Items[i].State == "RUNNING" {
				v.Items[i].State = "INTERRUPTED"
				v.Items[i].Code = "MATCH_INTERRUPTED"
			}
		}
		if err = writeRun(ctx, tx, user, &v); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE match_runs SET token='',lease_until=NULL WHERE id=?", v.ID); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) CreateMatchRun(ctx context.Context, user, requestKey, token string, v MatchRun) (MatchRun, bool, error) {
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`).MatchString(requestKey) || len(v.Items) < 1 || len(v.Items) > 100 || len(token) != 32 {
		return v, false, ErrValidation
	}
	replay := false
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		if err := lockRunUser(ctx, tx, user); err != nil {
			return err
		}
		if err := recoverRuns(ctx, tx, user); err != nil {
			return err
		}
		old, err := One[MatchRun](ctx, tx, "SELECT body FROM match_runs WHERE user_id=? AND request_key=? FOR UPDATE", user, requestKey)
		if err == nil {
			if old.RequestHash != v.RequestHash {
				return ErrConflict
			}
			v = old
			replay = true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var active int
		if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM match_runs WHERE user_id=? AND state IN ('RUNNING','PAUSING')", user).Scan(&active); err != nil {
			return err
		}
		if active > 0 {
			return ErrMatchRunBusy
		}
		v.RequestKey = requestKey
		v.ID = d.ID()
		v.State = "RUNNING"
		v.Version = 1
		v.CreatedAt = time.Now().UTC()
		v.UpdatedAt = v.CreatedAt
		_, err = tx.ExecContext(ctx, "INSERT INTO match_runs(id,user_id,request_key,state,token,lease_until,updated_at,body) VALUES(?,?,?,?,?,DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 60 SECOND),?,?)", v.ID, user, requestKey, v.State, token, v.UpdatedAt, d.JSON(v))
		return err
	})
	return v, replay, err
}
func (s *Store) MatchRuns(ctx context.Context, user string) ([]MatchRun, error) {
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		if err := lockRunUser(ctx, tx, user); err != nil {
			return err
		}
		return recoverRuns(ctx, tx, user)
	})
	if err != nil {
		return nil, err
	}
	return Many[MatchRun](ctx, s.DB, "SELECT body FROM match_runs WHERE user_id=? ORDER BY updated_at DESC,id DESC LIMIT 20", user)
}
func (s *Store) MatchRun(ctx context.Context, user, id string) (MatchRun, error) {
	return One[MatchRun](ctx, s.DB, "SELECT body FROM match_runs WHERE user_id=? AND id=?", user, id)
}
func (s *Store) MatchRunEvents(ctx context.Context, user, id string) ([]MatchRunEvent, error) {
	if _, err := s.MatchRun(ctx, user, id); err != nil {
		return nil, err
	}
	return Many[MatchRunEvent](ctx, s.DB, "SELECT body FROM match_run_events WHERE run_id=? ORDER BY occurred_at DESC,id DESC LIMIT 200", id)
}
func (s *Store) HydrateMatchRun(ctx context.Context, user string, v *MatchRun) error {
	ids := []string{}
	args := []any{user}
	for i := range v.Items {
		v.Items[i].Job = nil
		ids = append(ids, "?")
		args = append(args, v.Items[i].JobID)
	}
	if len(ids) == 0 {
		return nil
	}
	jobs, err := Many[ApplicationJob](ctx, s.DB, "SELECT body FROM jobs WHERE (visibility='GLOBAL' OR (visibility='PRIVATE' AND owner_id=?)) AND id IN ("+strings.Join(ids, ",")+")", args...)
	if err != nil {
		return err
	}
	byID := map[string]ApplicationJob{}
	for _, j := range jobs {
		byID[j.ID] = j
	}
	for i := range v.Items {
		if j, ok := byID[v.Items[i].JobID]; ok {
			copy := j
			v.Items[i].Job = &copy
		}
	}
	return nil
}
func (s *Store) RenewMatchRun(ctx context.Context, user string, g MatchRunGuard) error {
	res, err := s.DB.ExecContext(ctx, "UPDATE match_runs SET lease_until=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 60 SECOND) WHERE id=? AND user_id=? AND token=? AND state IN ('RUNNING','PAUSING') AND lease_until>UTC_TIMESTAMP(6)", g.ID, user, g.Token)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n != 1 {
		return ErrMatchRunLease
	}
	return err
}
func (s *Store) UpdateMatchRun(ctx context.Context, user string, g MatchRunGuard, fn func(*MatchRun) error) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if err := lockRunUser(ctx, tx, user); err != nil {
			return err
		}
		ctx = WithMatchRunGuard(ctx, g)
		if err := checkMatchRunGuard(ctx, tx, user, "", ""); err != nil {
			return err
		}
		v, err := One[MatchRun](ctx, tx, "SELECT body FROM match_runs WHERE id=? AND user_id=? FOR UPDATE", g.ID, user)
		if err != nil {
			return err
		}
		if err = fn(&v); err != nil {
			return err
		}
		return writeRun(ctx, tx, user, &v)
	})
}
func (s *Store) AddMatchRunEvent(ctx context.Context, user string, g MatchRunGuard, event MatchRunEvent) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		ctx = WithMatchRunGuard(ctx, g)
		if err := checkMatchRunGuard(ctx, tx, user, "", ""); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM match_run_events WHERE run_id=?", g.ID).Scan(&count); err != nil {
			return err
		}
		if count >= 2000 {
			return nil
		}
		event.ID = d.ID()
		event.RunID = g.ID
		event.OccurredAt = time.Now().UTC()
		_, err := tx.ExecContext(ctx, "INSERT INTO match_run_events(id,run_id,occurred_at,body) VALUES(?,?,?,?)", event.ID, g.ID, event.OccurredAt, d.JSON(event))
		return err
	})
}
func (s *Store) ControlMatchRun(ctx context.Context, user, id string, version int, action string) (MatchRun, error) {
	var v MatchRun
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		if err := lockRunUser(ctx, tx, user); err != nil {
			return err
		}
		if err := recoverRuns(ctx, tx, user); err != nil {
			return err
		}
		var err error
		v, err = One[MatchRun](ctx, tx, "SELECT body FROM match_runs WHERE id=? AND user_id=? FOR UPDATE", id, user)
		if err != nil {
			return err
		}
		if v.Version != version {
			return ErrConflict
		}
		switch action {
		case "PAUSE":
			if v.State != "RUNNING" {
				return ErrConflict
			}
			v.State = "PAUSING"
		case "CANCEL":
			if v.State == "COMPLETED" || v.State == "CANCELLED" {
				return ErrConflict
			}
			v.State = "CANCELLED"
			for i := range v.Items {
				if v.Items[i].State == "RUNNING" || v.Items[i].State == "QUEUED" {
					v.Items[i].State = "CANCELLED"
				}
			}
			if _, err = tx.ExecContext(ctx, "UPDATE match_runs SET token='',lease_until=NULL WHERE id=?", id); err != nil {
				return err
			}
		default:
			return ErrValidation
		}
		return writeRun(ctx, tx, user, &v)
	})
	return v, err
}
func (s *Store) ResumeMatchRun(ctx context.Context, user, id, token string, version int, candidateHash, model string, items []MatchRunItem) (MatchRun, error) {
	var v MatchRun
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		if err := lockRunUser(ctx, tx, user); err != nil {
			return err
		}
		if err := recoverRuns(ctx, tx, user); err != nil {
			return err
		}
		var err error
		v, err = One[MatchRun](ctx, tx, "SELECT body FROM match_runs WHERE id=? AND user_id=? FOR UPDATE", id, user)
		if err != nil {
			return err
		}
		if v.Version != version || v.State == "RUNNING" || v.State == "PAUSING" || v.State == "CANCELLED" || v.State == "COMPLETED" {
			return ErrConflict
		}
		if candidateHash != v.CandidateHash || model != v.Model || (v.Kind == "COMPANY" && len(items) != len(v.Items)) {
			return ErrStaleInput
		}
		var active int
		if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM match_runs WHERE user_id=? AND state IN ('RUNNING','PAUSING')", user).Scan(&active); err != nil {
			return err
		}
		if active > 0 {
			return ErrMatchRunBusy
		}
		updated := map[string]MatchRunItem{}
		for _, i := range items {
			updated[i.JobID] = i
		}
		for i := range v.Items {
			if next, ok := updated[v.Items[i].JobID]; ok {
				if ((v.Items[i].State == "SUCCEEDED" || v.Items[i].State == "REUSED") && v.Kind != "COMPANY") || v.Items[i].InputKey != next.InputKey {
					return ErrStaleInput
				}
				v.Items[i].State = "QUEUED"
				v.Items[i].Code = ""
				v.Items[i].Stage = ""
				v.Items[i].Diagnostic = nil
				delete(updated, v.Items[i].JobID)
			} else if v.Items[i].State == "QUEUED" {
				v.Items[i].State = "PAUSED"
			}
		}
		if len(updated) > 0 || len(items) == 0 {
			return ErrValidation
		}
		v.State = "RUNNING"
		if err = writeRun(ctx, tx, user, &v); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE match_runs SET token=?,lease_until=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 60 SECOND) WHERE id=?", token, id)
		return err
	})
	return v, err
}

// Bind every outbound description to the reviewed per-job input, even if it
// changes between queue preparation and the engine's own snapshot read.
func (s *Store) CheckMatchBatchInputs(ctx context.Context, user string, keys map[string]string) error {
	g, ok := ctx.Value(matchRunGuardKey{}).(MatchRunGuard)
	if !ok {
		return nil
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if err := checkMatchRunGuard(ctx, tx, user, "", ""); err != nil {
			return err
		}
		v, err := One[MatchRun](ctx, tx, "SELECT body FROM match_runs WHERE id=? AND user_id=?", g.ID, user)
		if err != nil {
			return err
		}
		for job, key := range keys {
			found := false
			for _, i := range v.Items {
				if i.JobID == job && i.State == "RUNNING" && i.InputKey == key {
					found = true
					break
				}
			}
			if !found {
				return ErrStaleInput
			}
		}
		return nil
	})
}

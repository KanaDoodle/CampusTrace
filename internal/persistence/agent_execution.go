package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

type ExecutionCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"arguments"`
}
type ExecutionObservation struct {
	Tool string          `json:"tool"`
	Data json.RawMessage `json:"data"`
}
type ExecutionFailure struct {
	Tool     string `json:"tool"`
	Code     string `json:"code"`
	Next     string `json:"next_step"`
	Attempts int    `json:"attempts"`
}
type AgentExecution struct {
	ID             string                 `json:"id"`
	SkillID        string                 `json:"skill_id"`
	SkillVersion   string                 `json:"skill_version"`
	HarnessVersion string                 `json:"harness_version"`
	RequestKey     string                 `json:"request_key"`
	Fingerprint    string                 `json:"fingerprint"`
	Input          json.RawMessage        `json:"input"`
	Identity       string                 `json:"input_identity"`
	MemoryRevision uint64                 `json:"memory_revision"`
	Next           int                    `json:"next_step"`
	Calls          []ExecutionCall        `json:"plan"`
	Observations   []ExecutionObservation `json:"grounded_observations"`
	Failures       []ExecutionFailure     `json:"failures"`
	State          string                 `json:"state"`
	Answer         string                 `json:"answer"`
	Resumes        int                    `json:"resumes"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
	LeaseUntil     *time.Time             `json:"lease_until,omitempty"`
	CanResume      bool                   `json:"can_resume"`
}

const MaxAgentExecutionAttempts = 5

// Continuation follows the database lease, rather than a browser's clock or
// a timestamp copied into a response before its final checkpoint was saved.
func (s *Store) readAgentExecutions(ctx context.Context, query string, args ...any) ([]AgentExecution, error) {
	rows, e := s.DB.QueryContext(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []AgentExecution{}
	for rows.Next() {
		var raw []byte
		var lease sql.NullTime
		if e := rows.Scan(&raw, &lease); e != nil {
			return nil, e
		}
		var v AgentExecution
		if e := json.Unmarshal(raw, &v); e != nil {
			return nil, e
		}
		v.LeaseUntil = nil
		if lease.Valid {
			v.LeaseUntil = &lease.Time
		}
		v.CanResume = (v.State == "PENDING" || v.State == "PAUSED" || v.State == "RUNNING") && v.Resumes < MaxAgentExecutionAttempts && (!lease.Valid || !lease.Time.After(time.Now()))
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) AgentExecutions(ctx context.Context, user string) ([]AgentExecution, error) {
	rows, e := s.readAgentExecutions(ctx, "SELECT body,lease_until FROM agent_executions WHERE user_id=? ORDER BY created_at DESC,id LIMIT 20", user)
	for i := range rows {
		rows[i].Observations = nil
		rows[i].Input = nil
		if text := []rune(rows[i].Answer); len(text) > 500 {
			rows[i].Answer = string(text[:500]) + "…"
		}
	}
	return rows, e
}
func (s *Store) AgentExecution(ctx context.Context, user, id string) (AgentExecution, error) {
	rows, e := s.readAgentExecutions(ctx, "SELECT body,lease_until FROM agent_executions WHERE id=? AND user_id=?", id, user)
	if e != nil {
		return AgentExecution{}, e
	}
	if len(rows) == 0 {
		return AgentExecution{}, ErrNotFound
	}
	return rows[0], nil
}
func (s *Store) BeginAgentExecution(ctx context.Context, user string, v AgentExecution) (AgentExecution, error) {
	var out AgentExecution
	e := s.Tx(ctx, func(tx *sql.Tx) error {
		if e := lockRunUser(ctx, tx, user); e != nil {
			return e
		}
		old, e := One[AgentExecution](ctx, tx, "SELECT body FROM agent_executions WHERE user_id=? AND request_key=? FOR UPDATE", user, v.RequestKey)
		if e == nil {
			if old.Fingerprint != v.Fingerprint {
				return ErrConflict
			}
			out = old
			return nil
		}
		if !errors.Is(e, ErrNotFound) {
			return e
		}
		var n int
		if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_executions WHERE user_id=? AND created_at>UTC_TIMESTAMP(6)-INTERVAL 1 HOUR", user).Scan(&n); e != nil {
			return e
		}
		if n >= 30 {
			return ErrValidation
		}
		v.ID = d.ID()
		v.State = "PENDING"
		v.CreatedAt = time.Now().UTC()
		v.UpdatedAt = v.CreatedAt
		v.Observations = []ExecutionObservation{}
		v.Failures = []ExecutionFailure{}
		_, e = tx.ExecContext(ctx, "INSERT INTO agent_executions(id,user_id,request_key,fingerprint,state,created_at,body) VALUES(?,?,?,?,?,?,?)", v.ID, user, v.RequestKey, v.Fingerprint, v.State, v.CreatedAt, d.JSON(v))
		out = v
		return e
	})
	return out, e
}
func (s *Store) ClaimAgentExecution(ctx context.Context, user, id string) (AgentExecution, string, error) {
	var v AgentExecution
	token := d.ID()
	e := s.Tx(ctx, func(tx *sql.Tx) error {
		if e := lockRunUser(ctx, tx, user); e != nil {
			return e
		}
		var lease sql.NullTime
		var raw []byte
		if e := tx.QueryRowContext(ctx, "SELECT body,lease_until FROM agent_executions WHERE id=? AND user_id=? FOR UPDATE", id, user).Scan(&raw, &lease); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return ErrNotFound
			}
			return e
		}
		if e := json.Unmarshal(raw, &v); e != nil {
			return e
		}
		if v.State == "COMPLETED" || v.State == "STALE" || v.State == "CANCELLED" || v.State == "FAILED" {
			token = ""
			return nil
		}
		if lease.Valid && lease.Time.After(time.Now()) {
			return ErrConflict
		}
		var running int
		if e := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_executions WHERE user_id=? AND id<>? AND state='RUNNING' AND lease_until>UTC_TIMESTAMP(6)", user, id).Scan(&running); e != nil {
			return e
		}
		if running >= 2 {
			return ErrConflict
		}
		if v.Resumes >= MaxAgentExecutionAttempts {
			v.State = "FAILED"
			v.Answer = "已达到本次任务的尝试上限，请核对原因后发起新任务。"
			token = ""
			_, e := tx.ExecContext(ctx, "UPDATE agent_executions SET state='FAILED',lease_token='',lease_until=NULL,body=? WHERE id=? AND user_id=?", d.JSON(v), id, user)
			return e
		}
		v.Resumes++
		v.State = "RUNNING"
		v.UpdatedAt = time.Now().UTC()
		_, e := tx.ExecContext(ctx, "UPDATE agent_executions SET state=?,lease_token=?,lease_until=?,body=? WHERE id=? AND user_id=?", v.State, token, v.UpdatedAt.Add(60*time.Second), d.JSON(v), id, user)
		return e
	})
	return v, token, e
}
func (s *Store) CheckpointAgentExecution(ctx context.Context, user, token string, v AgentExecution) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if e := lockRunUser(ctx, tx, user); e != nil {
			return e
		}
		rev, e := memoryRevision(ctx, tx, user)
		if e != nil {
			return e
		}
		if rev != v.MemoryRevision {
			return ErrConflict
		}
		v.UpdatedAt = time.Now().UTC()
		until := any(v.UpdatedAt.Add(60 * time.Second))
		if v.State != "RUNNING" {
			until = nil
		}
		r, e := tx.ExecContext(ctx, "UPDATE agent_executions SET state=?,lease_until=?,body=? WHERE id=? AND user_id=? AND lease_token=? AND state='RUNNING'", v.State, until, d.JSON(v), v.ID, user, token)
		if e != nil {
			return e
		}
		n, _ := r.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		return nil
	})
}
func (s *Store) CancelAgentExecution(ctx context.Context, user, id string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if e := lockRunUser(ctx, tx, user); e != nil {
			return e
		}
		v, e := One[AgentExecution](ctx, tx, "SELECT body FROM agent_executions WHERE id=? AND user_id=? FOR UPDATE", id, user)
		if e != nil {
			return e
		}
		if v.State == "COMPLETED" || v.State == "STALE" {
			return ErrConflict
		}
		v.State = "CANCELLED"
		v.UpdatedAt = time.Now().UTC()
		_, e = tx.ExecContext(ctx, "UPDATE agent_executions SET state='CANCELLED',lease_token='',lease_until=NULL,body=? WHERE id=? AND user_id=?", d.JSON(v), id, user)
		return e
	})
}

// Bind checkpoints to current reviewed inputs and the source snapshots they use.
func (s *Store) AgentExecutionIdentity(ctx context.Context, user, company, job string, scope ...string) (string, uint64, error) {
	rev, notes, profile, e := s.AgentContext(ctx, user)
	if e != nil {
		return "", 0, e
	}
	rows, e := Many[map[string]any](ctx, s.DB, `SELECT JSON_OBJECT('id',j.id,'revision',JSON_EXTRACT(j.body,'$.revision'),'observed',(SELECT JSON_OBJECT('hash',JSON_EXTRACT(o.body,'$.normalized_content_hash'),'at',o.observed_at) FROM observations o WHERE o.job_id=j.id ORDER BY o.observed_at DESC,o.id DESC LIMIT 1)) FROM jobs j WHERE (j.visibility='GLOBAL' OR j.owner_id=?) AND (?='' OR JSON_UNQUOTE(JSON_EXTRACT(j.body,'$.company'))=?) AND (?='' OR j.id=?) ORDER BY j.id LIMIT 10001`, user, company, company, job, job)
	if e != nil {
		return "", 0, e
	}
	if len(rows) > 10000 {
		return "", 0, ErrValidation
	}
	// Bind every reused tool observation to its mutable source, including new
	// match results, reviews and learning documents (not only the résumé/JD).
	tables := []struct{ table, id string }{}
	if len(scope) > 0 {
		switch scope[0] {
		case "company-choice":
			tables = append(tables, struct{ table, id string }{"job_match_results", "job_id"}, struct{ table, id string }{"company_match_reports", "input_key"})
		case "interview-prep", "review-plan":
			for _, table := range []string{"interviews", "reviews", "weak_topics", "practice_runs", "chunks"} {
				tables = append(tables, struct{ table, id string }{table, "id"})
			}
			if scope[0] == "interview-prep" {
				tables = append(tables, struct{ table, id string }{"job_match_results", "job_id"})
			}
		case "daily-review":
			tables = append(tables, struct{ table, id string }{"applications", "id"}, struct{ table, id string }{"agent_todos", "id"})
		}
	}
	versions := []any{}
	if len(scope) > 0 && scope[0] == "daily-review" {
		var enabled bool
		if e := s.DB.QueryRowContext(ctx, "SELECT enabled FROM agent_feed_settings WHERE user_id=?", user).Scan(&enabled); e != nil && !errors.Is(e, sql.ErrNoRows) {
			return "", 0, e
		}
		versions = append(versions, enabled)
	}
	for _, table := range tables {
		v, e := Many[map[string]any](ctx, s.DB, "SELECT JSON_OBJECT('id',"+table.id+",'hash',SHA2(CAST(body AS CHAR),256)) FROM "+table.table+" WHERE user_id=? ORDER BY "+table.id+" LIMIT 10001", user)
		if e != nil {
			return "", 0, e
		}
		if len(v) > 10000 {
			return "", 0, ErrValidation
		}
		versions = append(versions, v)
	}
	knowledge, e := s.KnowledgeIdentity(ctx, user)
	if e != nil {
		return "", 0, e
	}
	return d.Hash(d.JSON([]any{profile, rev, notes, rows, versions, knowledge, time.Now().UTC().Truncate(time.Hour)})), rev, nil
}

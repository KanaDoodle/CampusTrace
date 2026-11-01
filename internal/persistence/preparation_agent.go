package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/projectdefense"
)

func (s *Store) ClaimAgentPreparation(ctx context.Context, user, id string, claim NarrationClaim) (AgentExecution, string, error) {
	claim.Kind = d.PreparationAgentSkill
	return s.claimAgentExecution(ctx, user, id, &claim)
}

// Only reviewed records from this account participate in cross-application search.
type PreparationReview struct {
	ApplicationID string
	Review        d.Review
}

func (s *Store) RelatedPreparationReviews(ctx context.Context, user string) ([]PreparationReview, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT i.application_id,r.body FROM reviews r JOIN interviews i ON i.id=r.interview_id AND i.user_id=r.user_id WHERE r.user_id=? ORDER BY JSON_UNQUOTE(JSON_EXTRACT(i.body,'$.scheduled_at')) DESC,r.id LIMIT 50`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PreparationReview{}
	for rows.Next() {
		var v PreparationReview
		var raw []byte
		if err := rows.Scan(&v.ApplicationID, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &v.Review); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type SavePreparationArgs struct {
	EvidenceVersion    string `json:"evidence_version,omitempty"`
	ExecutionID        string `json:"execution_id"`
	Revision           int    `json:"revision"`
	PlanHash           string `json:"plan_hash"`
	ApplicationVersion int    `json:"application_version"`
}

func (a SavePreparationArgs) Validate() error {
	if len(a.ExecutionID) != 32 || len(a.PlanHash) != 64 || a.Revision < 1 || a.ApplicationVersion < 1 {
		return ErrValidation
	}
	return nil
}

func savePreparationTx(ctx context.Context, tx *sql.Tx, user string, a SavePreparationArgs) (json.RawMessage, error) {
	v, err := One[AgentExecution](ctx, tx, "SELECT body FROM agent_executions WHERE id=? AND user_id=? FOR UPDATE", a.ExecutionID, user)
	if err != nil {
		return nil, err
	}
	state := v.Preparation
	if state != nil && state.Defense != nil && (a.EvidenceVersion != state.Defense.Version || state.Defense.Version != projectdefense.Version()) {
		return nil, ErrStaleInput
	}
	if v.State != "COMPLETED" || state == nil || state.Plan == nil || state.Revision != a.Revision || d.Hash(d.JSON(state.Plan)) != a.PlanHash {
		return nil, ErrConflict
	}
	app, err := One[d.Application](ctx, tx, "SELECT body FROM applications WHERE id=? AND user_id=? FOR UPDATE", state.Input.ApplicationID, user)
	if err != nil {
		return nil, err
	}
	if app.Version != a.ApplicationVersion {
		return nil, ErrConflict
	}
	identity, _, err := preparationAgentIdentityQuery(ctx, tx, user, state.Input.ApplicationID, state.Input.OtherReviews)
	if err != nil {
		return nil, err
	}
	if identity != v.SourceIdentity {
		return nil, ErrStaleInput
	}

	if state.SavedAt == nil {
		now := time.Now().UTC()
		state.SavedAt = &now
		_, err = tx.ExecContext(ctx, "UPDATE agent_executions SET body=? WHERE id=? AND user_id=? AND state='COMPLETED'", d.JSON(v), v.ID, user)
		if err != nil {
			return nil, err
		}
	}
	return json.RawMessage(d.JSON(map[string]any{"execution_id": v.ID, "application_id": app.ID, "revision": state.Revision, "saved_at": state.SavedAt, "plan": state.Plan})), nil
}

func preparationAgentIdentityQuery(ctx context.Context, q Queryer, user, id string, other bool) (string, uint64, error) {
	base, rev, err := applicationPreparationIdentityQuery(ctx, q, user, id)
	if err != nil {
		return "", 0, err
	}
	extra := []map[string]any{}
	if other {
		extra, err = Many[map[string]any](ctx, q, `SELECT JSON_OBJECT('id',r.id,'hash',SHA2(CAST(r.body AS CHAR),256),'application_id',i.application_id) FROM reviews r JOIN interviews i ON i.id=r.interview_id AND i.user_id=r.user_id WHERE r.user_id=? ORDER BY JSON_UNQUOTE(JSON_EXTRACT(i.body,'$.scheduled_at')) DESC,r.id LIMIT 50`, user)
		if err != nil {
			return "", 0, err
		}
	}
	return d.Hash(d.JSON([]any{"preparation-agent-input-v1", base, other, extra})), rev, nil
}
func (s *Store) PreparationAgentIdentity(ctx context.Context, user, id string, other bool) (string, uint64, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()
	value, rev, err := preparationAgentIdentityQuery(ctx, tx, user, id, other)
	if err != nil {
		return "", 0, err
	}
	if err = tx.Commit(); err != nil {
		return "", 0, err
	}
	return value, rev, nil
}

func (s *Store) PreparationExecutions(ctx context.Context, user, application string) ([]AgentExecution, error) {
	if application != "" && len(application) != 32 {
		return nil, ErrValidation
	}
	rows, err := s.readAgentExecutions(ctx, `SELECT body,lease_until FROM agent_executions WHERE user_id=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.skill_id'))=? AND (?='' OR JSON_UNQUOTE(JSON_EXTRACT(body,'$.preparation_agent.input.application_id'))=?) ORDER BY created_at DESC,id LIMIT 20`, user, d.PreparationAgentSkill, application, application)
	for i := range rows {
		rows[i].Observations = nil
		rows[i].Input = nil
		if rows[i].Preparation != nil {
			s := *rows[i].Preparation
			s.Messages = nil
			s.Sources = nil
			s.Trace = nil
			s.Plan = nil
			s.PreviousPlan = nil
			rows[i].Preparation = &s
		}
	}
	return rows, err
}

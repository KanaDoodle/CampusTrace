package persistence

import (
	"context"
	"database/sql"
	"errors"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

// PreparationReviews never mixes another account's or another application's
// interviews into a preparation task. The job scope is used before applying.
func (s *Store) PreparationReviews(ctx context.Context, user, application, job string) ([]d.Review, error) {
	if (application == "") == (job == "") || (application != "" && len(application) != 32) || (job != "" && len(job) != 32) {
		return nil, ErrValidation
	}
	query := `SELECT r.body FROM reviews r
 JOIN interviews i ON i.id=r.interview_id AND i.user_id=r.user_id
 JOIN applications a ON a.id=i.application_id AND a.user_id=i.user_id
 WHERE r.user_id=?`
	args := []any{user}
	if application != "" {
		query += " AND a.id=?"
		args = append(args, application)
	} else {
		query += " AND a.job_id=?"
		args = append(args, job)
	}
	query += " ORDER BY JSON_UNQUOTE(JSON_EXTRACT(i.body,'$.scheduled_at')) DESC,r.id LIMIT 51"
	rows, err := Many[d.Review](ctx, s.DB, query, args...)
	if err == nil && len(rows) > 50 {
		return nil, ErrValidation
	}
	return rows, err
}

// Frozen application preparation deliberately excludes today's résumé and JD.
// Reviews, confirmed notices, memories and the knowledge corpus still fence
// checkpoints, so deleting a source cannot leave a resumable stale result.
func (s *Store) applicationPreparationIdentity(ctx context.Context, user, id string) (string, uint64, error) {
	if len(id) != 32 {
		return "", 0, ErrValidation
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()
	app, err := One[d.Application](ctx, tx, "SELECT body FROM applications WHERE id=? AND user_id=?", id, user)
	if err != nil {
		return "", 0, err
	}
	revision, err := memoryRevision(ctx, tx, user)
	if err != nil {
		return "", 0, err
	}
	notes, err := Many[AgentMemory](ctx, tx, "SELECT body FROM agent_memories WHERE user_id=? AND deleted=FALSE AND expires_at>UTC_TIMESTAMP(6) ORDER BY id LIMIT 100", user)
	if err != nil {
		return "", 0, err
	}
	parts := []any{"application-preparation-v1", app, revision, notes}
	for _, query := range []string{
		"SELECT JSON_OBJECT('id',phase,'hash',SHA2(CAST(body AS CHAR),256)) FROM application_contexts WHERE user_id=? AND application_id=? ORDER BY phase LIMIT 3",
		"SELECT JSON_OBJECT('id',id,'hash',SHA2(CAST(body AS CHAR),256)) FROM interviews WHERE user_id=? AND application_id=? ORDER BY id LIMIT 2001",
		"SELECT JSON_OBJECT('id',r.id,'hash',SHA2(CAST(r.body AS CHAR),256)) FROM reviews r JOIN interviews i ON i.id=r.interview_id AND i.user_id=r.user_id WHERE r.user_id=? AND i.application_id=? ORDER BY r.id LIMIT 2001",
		"SELECT JSON_OBJECT('id',id,'hash',SHA2(CAST(body AS CHAR),256)) FROM recruitment_notices WHERE user_id=? AND application_id=? ORDER BY id LIMIT 2001",
	} {
		rows, e := Many[map[string]any](ctx, tx, query, user, id)
		if e != nil {
			return "", 0, e
		}
		if len(rows) > 2000 {
			return "", 0, ErrValidation
		}
		parts = append(parts, rows)
	}
	var knowledge uint64
	err = tx.QueryRowContext(ctx, "SELECT revision FROM knowledge_state WHERE user_id=?", user).Scan(&knowledge)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", 0, err
	}
	parts = append(parts, knowledge)
	if err := tx.Commit(); err != nil {
		return "", 0, err
	}
	return d.Hash(d.JSON(parts)), revision, nil
}

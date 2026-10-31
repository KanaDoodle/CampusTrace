package persistence

import (
	"context"
	"database/sql"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

type queueBusyError struct{}

func (queueBusyError) Error() string   { return "durable task admission capacity reached" }
func (queueBusyError) Transient() bool { return true }

var ErrQueueBusy error = queueBusyError{}

// A durable fence precedes the external call. Absence of a response does not
// establish no charge. Abandoned invocations are never automatically replayed.
func (s *Store) BeginAnalysisInvocation(ctx context.Context, t Task) (bool, error) {
	r, e := s.DB.ExecContext(ctx, "INSERT IGNORE INTO analysis_invocations(observation_id,version_hash,task_id,started_at) VALUES(?,?,?,UTC_TIMESTAMP(6))", t.EntityID, d.Hash(t.ProcessingVersion), t.ID)
	if e != nil {
		return false, e
	}
	n, e := r.RowsAffected()
	return n == 1, e
}
func (s *Store) RejectAnalysisInvocation(ctx context.Context, t Task) error {
	_, e := s.DB.ExecContext(ctx, "DELETE FROM analysis_invocations WHERE observation_id=? AND version_hash=? AND task_id=?", t.EntityID, d.Hash(t.ProcessingVersion), t.ID)
	return e
}

func queueOwner(owner string) string {
	if owner == "" {
		return "system"
	}
	return owner
}
func taskOwner(ctx context.Context, q Queryer, t Task) (string, error) {
	var owner string
	var e error
	switch t.Type {
	case "WATCH_CHECK", "WATCH_FETCH":
		e = q.QueryRowContext(ctx, "SELECT user_id FROM watch_targets WHERE id=?", t.WatchID).Scan(&owner)
	case "SOURCE_IMPORT":
		e = q.QueryRowContext(ctx, "SELECT b.user_id FROM source_import_items i JOIN source_import_batches b ON b.id=i.batch_id WHERE i.id=?", t.EntityID).Scan(&owner)
	case "ANALYZE":
		e = q.QueryRowContext(ctx, "SELECT COALESCE(j.owner_id,'') FROM observations o JOIN jobs j ON j.id=o.job_id WHERE o.id=?", t.EntityID).Scan(&owner)
	case "ASSESS":
		e = q.QueryRowContext(ctx, "SELECT COALESCE(owner_id,'') FROM jobs WHERE id=?", t.EntityID).Scan(&owner)
	}
	if errors.Is(e, sql.ErrNoRows) {
		return "", nil
	}
	return owner, e
}
func (s *Store) TaskOwner(ctx context.Context, t Task) (string, error) {
	return taskOwner(ctx, s.DB, t)
}

// Backfill only unsent legacy rows, in small transactions, without expiring them.
func (s *Store) BackfillOutboxTenants(ctx context.Context) (int, error) {
	n := 0
	e := s.Tx(ctx, func(tx *sql.Tx) error {
		tasks, e := Many[Task](ctx, tx, "SELECT o.body FROM outbox o LEFT JOIN outbox_tenants f ON f.task_id=o.id WHERE o.sent=FALSE AND f.task_id IS NULL ORDER BY o.created_at,o.id LIMIT 100 FOR UPDATE SKIP LOCKED")
		if e != nil {
			return e
		}
		for _, t := range tasks {
			owner, e := taskOwner(ctx, tx, t)
			if e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, "INSERT IGNORE INTO outbox_tenants(task_id,owner_id,created_at) VALUES(?,?,?)", t.ID, queueOwner(owner), t.CreatedAt); e != nil {
				return e
			}
			n++
		}
		return nil
	})
	return n, e
}

// FairOutbox executes one oldest task per (owner, type) per turn. A database cursor
// serializes dispatch turns across instances. Callback must not acquire a user lock.
func (s *Store) FairOutbox(ctx context.Context, send func(context.Context, Task) (bool, error)) (int, error) {
	n := 0
	e := s.Tx(ctx, func(tx *sql.Tx) error {
		var cursor string
		e := tx.QueryRowContext(ctx, "SELECT last_owner FROM queue_dispatch_clock WHERE id='outbox' FOR UPDATE SKIP LOCKED").Scan(&cursor)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		read := func(after string) ([]string, error) {
			rows, e := tx.QueryContext(ctx, "SELECT DISTINCT f.owner_id FROM outbox_tenants f JOIN outbox o ON o.id=f.task_id WHERE o.sent=FALSE AND f.owner_id>? ORDER BY f.owner_id LIMIT 32", after)
			if e != nil {
				return nil, e
			}
			defer rows.Close()
			out := []string{}
			for rows.Next() {
				var id string
				if e = rows.Scan(&id); e != nil {
					return nil, e
				}
				out = append(out, id)
			}
			return out, rows.Err()
		}
		owners, e := read(cursor)
		if e != nil {
			return e
		}
		if len(owners) == 0 {
			owners, e = read("")
			if e != nil {
				return e
			}
		}
		for _, owner := range owners {
			for _, typ := range []string{"ANALYZE", "ASSESS", "WATCH_CHECK", "WATCH_FETCH", "SOURCE_IMPORT"} {
				t, e := One[Task](ctx, tx, "SELECT o.body FROM outbox o JOIN outbox_tenants f ON f.task_id=o.id WHERE o.sent=FALSE AND f.owner_id=? AND JSON_UNQUOTE(JSON_EXTRACT(o.body,'$.task_type'))=? ORDER BY o.created_at,o.id LIMIT 1 FOR UPDATE SKIP LOCKED", owner, typ)
				if errors.Is(e, sql.ErrNoRows) {
					continue
				}
				if e != nil {
					return e
				}
				if owner != "system" {
					t.OwnerID = owner
				}
				sent, e := send(ctx, t)
				if e != nil {
					return e
				}
				if sent {
					if _, e = tx.ExecContext(ctx, "UPDATE outbox SET sent=TRUE WHERE id=?", t.ID); e != nil {
						return e
					}
					n++
				}
			}
			cursor = owner
		}
		_, e = tx.ExecContext(ctx, "UPDATE queue_dispatch_clock SET last_owner=? WHERE id='outbox'", cursor)
		return e
	})
	return n, e
}

package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"time"
)

var ErrSourceImportCooldown = errors.New("source import cooldown")
var ErrStaleSourceImport = errors.New("stale source import generation")

type SourceImportSpec struct {
	Adapter string `json:"adapter"`
	Company string `json:"company"`
	URL     string `json:"url"`
	Scope   string `json:"scope"`
}
type SourceImportItem struct {
	SourceImportSpec
	ID         string `json:"id"`
	BatchID    string `json:"batch_id"`
	Generation uint64 `json:"generation"`
	State      string `json:"state"`
	Code       string `json:"code,omitempty"`
	SourceID   string `json:"source_id,omitempty"`
	WatchID    string `json:"watch_id,omitempty"`
	WatchProgress
}
type SourceImportBatch struct {
	ID        string             `json:"id"`
	CreatedAt time.Time          `json:"created_at"`
	State     string             `json:"state"`
	Existing  bool               `json:"existing,omitempty"`
	Completed int                `json:"completed"`
	Failed    int                `json:"failed"`
	Imported  int                `json:"imported"`
	Items     []SourceImportItem `json:"items"`
}

func SourceImportTaskID(id string, generation uint64) string {
	return d.Hash(fmt.Sprintf("source-import:%s:%d", id, generation))[:32]
}
func sourceImportTask(v SourceImportItem) Task {
	t := NewTask("SOURCE_IMPORT", v.ID)
	t.Generation = v.Generation
	t.ID = SourceImportTaskID(v.ID, v.Generation)
	return t
}
func sourceImportBatch(ctx context.Context, q Queryer, user, id string) (SourceImportBatch, error) {
	out := SourceImportBatch{Items: []SourceImportItem{}, State: "COMPLETED"}
	if err := q.QueryRowContext(ctx, "SELECT id,created_at FROM source_import_batches WHERE id=? AND user_id=?", id, user).Scan(&out.ID, &out.CreatedAt); err != nil {
		return out, err
	}
	rows, err := q.QueryContext(ctx, `SELECT i.body,w.body,COALESCE(r.expected_count,0),COALESCE(r.completed_count,0),COALESCE(r.failed_count,0)
 FROM source_import_items i LEFT JOIN watch_targets w ON w.id=i.watch_id
 LEFT JOIN watch_runs r ON r.watch_id=w.id AND r.schedule_version=CAST(JSON_UNQUOTE(JSON_EXTRACT(w.body,'$.schedule_version')) AS UNSIGNED)
 WHERE i.batch_id=? ORDER BY i.position`, id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v SourceImportItem
		var body, watchBody []byte
		var progress WatchProgress
		if err = rows.Scan(&body, &watchBody, &progress.Expected, &progress.Completed, &progress.Failed); err != nil {
			return out, err
		}
		if err = json.Unmarshal(body, &v); err != nil {
			return out, err
		}
		v.WatchProgress = progress
		if v.WatchID != "" {
			if len(watchBody) == 0 {
				v.State = "FAILED"
				v.Code = "SOURCE_IMPORT_INTERRUPTED"
			} else {
				var watch d.WatchTarget
				if err = json.Unmarshal(watchBody, &watch); err != nil {
					return out, err
				}
				switch watch.LastOutcome {
				case "SUCCESS":
					v.State = "SUCCESS"
					if v.Expected == 0 {
						v.State = "EMPTY"
					}
				case "FAILED":
					v.State = "FAILED"
					if v.Code == "" {
						v.Code = "SOURCE_IMPORT_DISCOVERY_FAILED"
					}
				case "PARTIAL_FAILURE":
					v.State = "FAILED"
					if v.Code == "" {
						v.Code = "SOURCE_IMPORT_PARTIAL_FAILURE"
					}
				case "RESTORED_PAUSED":
					v.State = "FAILED"
					v.Code = "SOURCE_IMPORT_INTERRUPTED"
				case "PROCESSING":
					v.State = "FETCHING"
				case "DISCOVERY_FAILED":
					v.State = "RETRYING"
				default:
					v.State = "QUEUED"
				}
			}
		}
		switch v.State {
		case "SUCCESS", "EMPTY":
			out.Completed++
		case "FAILED":
			out.Completed++
			out.Failed++
		default:
			out.State = "RUNNING"
		}
		out.Imported += v.WatchProgress.Completed - v.WatchProgress.Failed
		out.Items = append(out.Items, v)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if out.State == "COMPLETED" && out.Failed > 0 {
		out.State = "COMPLETED_WITH_ERRORS"
	}
	return out, nil
}
func (s *Store) LatestSourceImport(ctx context.Context, user string) (*SourceImportBatch, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, "SELECT id FROM source_import_batches WHERE user_id=? ORDER BY created_at DESC,id DESC LIMIT 1", user).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v, err := sourceImportBatch(ctx, s.DB, user, id)
	return &v, err
}

// One active batch per user. Selection comes from server presets, never request URLs.
func (s *Store) StartSourceImport(ctx context.Context, user string, specs []SourceImportSpec) (SourceImportBatch, error) {
	var out SourceImportBatch
	if len(specs) == 0 || len(specs) > 64 {
		return out, ErrValidation
	}
	seen := map[string]bool{}
	for _, v := range specs {
		if !d.IsCampusSource(v.Adapter) || seen[v.Adapter] || v.Company == "" || len(v.Company) > 160 || len(v.URL) > 2000 || len(v.Scope) > 500 {
			return out, ErrValidation
		}
		seen[v.Adapter] = true
	}
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var owner string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&owner); err != nil {
			return err
		}
		var latest string
		err := tx.QueryRowContext(ctx, "SELECT id FROM source_import_batches WHERE user_id=? ORDER BY created_at DESC,id DESC LIMIT 1", user).Scan(&latest)
		if err == nil {
			v, e := sourceImportBatch(ctx, tx, user, latest)
			if e != nil {
				return e
			}
			if v.State == "RUNNING" {
				out = v
				out.Existing = true
				return nil
			}
			if time.Since(v.CreatedAt) < 30*time.Minute {
				return ErrSourceImportCooldown
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		// Keep two previous completed batches plus the new batch. Deleting run receipts
		// does not remove imported jobs, observations, sources or periodic watches.
		ids, e := importBatchIDs(ctx, tx, user)
		if e != nil {
			return e
		}
		for _, id := range ids[min(2, len(ids)):] {
			for _, query := range []string{`DELETE FROM outbox WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.entity_id')) IN (SELECT id FROM source_import_items WHERE batch_id=?)`, `DELETE FROM outbox WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.watch_id')) IN (SELECT watch_id FROM source_import_items WHERE batch_id=?)`} {
				if _, e = tx.ExecContext(ctx, query, id); e != nil {
					return e
				}
			}
			watches, e := Many[d.WatchTarget](ctx, tx, `SELECT w.body FROM source_import_items i JOIN watch_targets w ON w.id=i.watch_id WHERE i.batch_id=?`, id)
			if e != nil {
				return e
			}
			for _, w := range watches {
				if w.OneShot {
					if _, e = tx.ExecContext(ctx, "DELETE FROM watch_targets WHERE id=?", w.ID); e != nil {
						return e
					}
				}
			}
			if _, e = tx.ExecContext(ctx, "DELETE FROM source_import_batches WHERE id=? AND user_id=?", id, user); e != nil {
				return e
			}
		}
		out = SourceImportBatch{ID: d.ID(), CreatedAt: time.Now().UTC(), State: "RUNNING", Items: []SourceImportItem{}}
		if _, err = tx.ExecContext(ctx, "INSERT INTO source_import_batches(id,user_id,created_at) VALUES(?,?,?)", out.ID, user, out.CreatedAt); err != nil {
			return err
		}
		for position, spec := range specs {
			v := SourceImportItem{SourceImportSpec: spec, ID: d.ID(), BatchID: out.ID, Generation: 1, State: "PENDING"}
			if _, err = tx.ExecContext(ctx, "INSERT INTO source_import_items(id,batch_id,position,body) VALUES(?,?,?,?)", v.ID, out.ID, position, d.JSON(v)); err != nil {
				return err
			}
			if err = Outbox(ctx, tx, sourceImportTask(v)); err != nil {
				return err
			}
			out.Items = append(out.Items, v)
		}
		return nil
	})
	return out, err
}
func importBatchIDs(ctx context.Context, q Queryer, user string) ([]string, error) {
	rows, err := q.QueryContext(ctx, "SELECT id FROM source_import_batches WHERE user_id=? ORDER BY created_at DESC,id DESC", user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func importItemForTask(ctx context.Context, q Queryer, t Task, lock string) (SourceImportItem, error) {
	v, err := One[SourceImportItem](ctx, q, "SELECT body FROM source_import_items WHERE id=?"+lock, t.EntityID)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrStaleSourceImport
	}
	if err != nil {
		return v, err
	}
	if t.Type != "SOURCE_IMPORT" || t.Generation != v.Generation || t.ID != SourceImportTaskID(v.ID, v.Generation) || (v.State != "PENDING" && v.State != "PREPARING") {
		return v, ErrStaleSourceImport
	}
	return v, nil
}
func (s *Store) PrepareSourceImport(ctx context.Context, t Task) (SourceImportItem, error) {
	var v SourceImportItem
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var e error
		v, e = importItemForTask(ctx, tx, t, " FOR UPDATE")
		if e != nil {
			return e
		}
		v.State = "PREPARING"
		return saveImportItem(ctx, tx, v)
	})
	return v, err
}
func saveImportItem(ctx context.Context, tx *sql.Tx, v SourceImportItem) error {
	var watch any
	if v.WatchID != "" {
		watch = v.WatchID
	}
	_, err := tx.ExecContext(ctx, "UPDATE source_import_items SET watch_id=?,body=? WHERE id=?", watch, d.JSON(v), v.ID)
	return err
}
func (s *Store) QueueSourceImport(ctx context.Context, t Task, adapter, tenant, name string) error {
	// Lock order: user, item, watch. Network discovery happens before this transaction.
	var user string
	if err := s.DB.QueryRowContext(ctx, "SELECT b.user_id FROM source_import_batches b JOIN source_import_items i ON i.batch_id=b.id WHERE i.id=?", t.EntityID).Scan(&user); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if err := validateCampusSource(user, adapter, tenant, name); err != nil {
		return err
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var owner string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&owner); err != nil {
			return err
		}
		v, err := importItemForTask(ctx, tx, t, " FOR UPDATE")
		if errors.Is(err, ErrStaleSourceImport) {
			return nil
		}
		if err != nil {
			return err
		}
		if v.Adapter != adapter {
			return ErrValidation
		}
		src, err := campusSourceTx(ctx, tx, user, adapter, tenant, name)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		watch := d.WatchTarget{ID: d.ID(), UserID: user, OneShot: true, WatchInput: d.WatchInput{SourceID: src.ID, CheckInterval: 21600, Enabled: true}, ScheduleVersion: 1, LastOutcome: "QUEUED", RoundStartedAt: now, NextCheckAt: now, CreatedAt: now, UpdatedAt: now}
		if _, err = tx.ExecContext(ctx, "INSERT INTO watch_targets(id,user_id,source_id,enabled,next_check_at,body) VALUES(?,?,?,?,?,?)", watch.ID, user, src.ID, true, now, d.JSON(watch)); err != nil {
			return err
		}
		v.State = "IMPORTING"
		v.WatchID = watch.ID
		v.SourceID = src.ID
		v.Code = ""
		if err = saveImportItem(ctx, tx, v); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT IGNORE INTO completed_tasks(id,completed_at) VALUES(?,?)", t.ID, time.Now().UTC()); err != nil {
			return err
		}
		return Outbox(ctx, tx, WatchTask(watch))
	})
}
func (s *Store) FailSourceImport(ctx context.Context, t Task, code string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		v, err := importItemForTask(ctx, tx, t, " FOR UPDATE")
		if errors.Is(err, ErrStaleSourceImport) {
			return nil
		}
		if err != nil {
			return err
		}
		v.State = "FAILED"
		v.Code = code
		if _, err = tx.ExecContext(ctx, "INSERT IGNORE INTO completed_tasks(id,completed_at) VALUES(?,?)", t.ID, time.Now().UTC()); err != nil {
			return err
		}
		return saveImportItem(ctx, tx, v)
	})
}
func (s *Store) RetrySourceImport(ctx context.Context, user, id string) (SourceImportBatch, error) {
	var out SourceImportBatch
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var owner string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&owner); err != nil {
			return err
		}
		var latest string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM source_import_batches WHERE user_id=? ORDER BY created_at DESC,id DESC LIMIT 1", user).Scan(&latest); err != nil {
			return err
		}
		if latest != id {
			return ErrNotFound
		}
		v, err := sourceImportBatch(ctx, tx, user, id)
		if err != nil {
			return err
		}
		for _, item := range v.Items {
			if item.State != "FAILED" {
				continue
			}
			stored, err := One[SourceImportItem](ctx, tx, "SELECT body FROM source_import_items WHERE id=? FOR UPDATE", item.ID)
			if err != nil {
				return err
			}
			if stored.WatchID != "" {
				watch, err := One[d.WatchTarget](ctx, tx, "SELECT body FROM watch_targets WHERE id=? AND user_id=? FOR UPDATE", stored.WatchID, user)
				if errors.Is(err, sql.ErrNoRows) {
					stored.WatchID = ""
					stored.SourceID = ""
				} else if err != nil {
					return err
				} else {
					// Recheck under the watch lock: concurrent completion/retry cannot enqueue twice.
					if watch.Enabled {
						continue
					}
					if !watch.OneShot {
						return ErrValidation
					}
					now := time.Now().UTC()
					watch.Enabled = true
					watch.ScheduleVersion++
					watch.LastOutcome = "QUEUED"
					watch.RoundStartedAt = now
					watch.UpdatedAt = now
					watch.RoundChanged = false
					watch.ScheduleReason = ""
					watch.DiscoveryHash = ""
					if err = saveWatch(ctx, tx, watch); err != nil {
						return err
					}
					if err = Outbox(ctx, tx, WatchTask(watch)); err != nil {
						return err
					}
				}
			}
			stored.Code = ""
			stored.Generation++
			if stored.WatchID == "" {
				stored.State = "PENDING"
				if err = Outbox(ctx, tx, sourceImportTask(stored)); err != nil {
					return err
				}
			} else {
				stored.State = "IMPORTING"
			}
			if err = saveImportItem(ctx, tx, stored); err != nil {
				return err
			}
		}
		out, err = sourceImportBatch(ctx, tx, user, id)
		return err
	})
	return out, err
}

// Save only a safe failure category, fenced by the current watch generation.
// This runs after the terminal watch transaction, keeping the user/item/watch
// lock order used by retries intact.
func (s *Store) SourceImportWatchFailed(ctx context.Context, t Task, code string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE source_import_items i JOIN watch_targets w ON w.id=i.watch_id SET i.body=JSON_SET(i.body,'$.code',?) WHERE w.id=? AND CAST(JSON_UNQUOTE(JSON_EXTRACT(w.body,'$.schedule_version')) AS UNSIGNED)=?`, code, t.WatchID, t.ScheduleVersion)
	return err
}

package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/migrations"
	"strings"
	"time"
)

var ErrStaleWatch = errors.New("stale watch generation")

func (s *Store) migrateRadar(ctx context.Context) error {
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var locked int
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(CONCAT(DATABASE(),':radar-v4'),30)").Scan(&locked); err != nil {
		return err
	}
	if locked != 1 {
		return ErrBackendUnavailable
	}
	defer conn.ExecContext(context.Background(), "DO RELEASE_LOCK(CONCAT(DATABASE(),':radar-v4'))")
	for _, stmt := range strings.Split(migrations.RadarSQL, ";") {
		if strings.TrimSpace(stmt) != "" {
			if _, err = conn.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Store) SourcesForUser(ctx context.Context, user string) ([]d.Source, error) {
	if user == "" {
		return nil, ErrNotFound
	}
	return Many[d.Source](ctx, s.DB, "SELECT body FROM sources WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.visibility'))='GLOBAL' OR JSON_UNQUOTE(JSON_EXTRACT(body,'$.owner_id'))=? ORDER BY id LIMIT 100", user)
}
func watchSource(ctx context.Context, q Queryer, user, id string) (d.Source, error) {
	src, err := One[d.Source](ctx, q, "SELECT body FROM sources WHERE id=?", id)
	if err != nil {
		return src, err
	}
	if user == "" || (src.Visibility != "GLOBAL" && src.OwnerID != user) {
		return src, ErrNotFound
	}
	if src.Adapter == "" || src.Tenant == "" {
		return src, fmt.Errorf("validation: unsupported source discovery")
	}
	return src, nil
}
func createWatchTx(ctx context.Context, tx *sql.Tx, user string, input d.WatchInput) (d.WatchTarget, error) {
	var v d.WatchTarget
	if err := input.Validate(); err != nil {
		return v, err
	}
	src, err := watchSource(ctx, tx, user, input.SourceID)
	if err != nil {
		return v, err
	}
	if input.CheckInterval < d.SourceMinimumInterval(src.Adapter) || ((src.Adapter == "baidu" || src.Adapter == "meituan") && input.Direction != "") {
		return v, ErrValidation
	}
	var owner string
	if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&owner); err != nil {
		return v, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM watch_targets WHERE user_id=?", user).Scan(&count); err != nil {
		return v, err
	}
	if count >= 100 {
		return v, ErrValidation
	}
	now := time.Now().UTC()
	v = d.WatchTarget{ID: d.ID(), UserID: user, WatchInput: input, NextCheckAt: now, CreatedAt: now, UpdatedAt: now, LastOutcome: "PENDING"}
	_, err = tx.ExecContext(ctx, "INSERT INTO watch_targets(id,user_id,source_id,enabled,next_check_at,body) VALUES(?,?,?,?,?,?)", v.ID, user, input.SourceID, input.Enabled, now, d.JSON(v))
	return v, err
}
func (s *Store) CreateWatch(ctx context.Context, user string, input d.WatchInput) (d.WatchTarget, error) {
	var v d.WatchTarget
	err := s.Tx(ctx, func(tx *sql.Tx) error { var err error; v, err = createWatchTx(ctx, tx, user, input); return err })
	return v, err
}
func (s *Store) Watch(ctx context.Context, user, id string) (d.WatchTarget, error) {
	return One[d.WatchTarget](ctx, s.DB, "SELECT body FROM watch_targets WHERE id=? AND user_id=?", id, user)
}
func (s *Store) Watches(ctx context.Context, user string) ([]d.WatchTarget, error) {
	if user == "" {
		return nil, ErrNotFound
	}
	return Many[d.WatchTarget](ctx, s.DB, "SELECT body FROM watch_targets WHERE user_id=? ORDER BY id LIMIT 100", user)
}
func saveWatch(ctx context.Context, tx *sql.Tx, v d.WatchTarget) error {
	_, err := tx.ExecContext(ctx, "UPDATE watch_targets SET enabled=?,next_check_at=?,body=? WHERE id=?", v.Enabled, v.NextCheckAt, d.JSON(v), v.ID)
	return err
}
func (s *Store) UpdateWatch(ctx context.Context, user, id string, input d.WatchInput) (d.WatchTarget, error) {
	var v d.WatchTarget
	if err := input.Validate(); err != nil {
		return v, err
	}
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		v, err = One[d.WatchTarget](ctx, tx, "SELECT body FROM watch_targets WHERE id=? AND user_id=? FOR UPDATE", id, user)
		if err != nil {
			return err
		}
		if input.SourceID != v.SourceID {
			return ErrValidation
		}
		src, err := watchSource(ctx, tx, user, input.SourceID)
		if err != nil {
			return err
		}
		if input.CheckInterval < d.SourceMinimumInterval(src.Adapter) || ((src.Adapter == "baidu" || src.Adapter == "meituan") && input.Direction != "") {
			return ErrValidation
		}
		if input.Keyword != v.Keyword || input.Direction != v.Direction {
			if _, err = tx.ExecContext(ctx, "DELETE FROM watch_postings WHERE watch_id=?", v.ID); err != nil {
				return err
			}
		}
		v.WatchInput = input
		v.StableRounds, v.FailureRounds, v.EffectiveInterval = 0, 0, 0
		v.RoundChanged = false
		v.RoundStartedAt = time.Time{}
		v.DiscoveryHash = ""
		v.ScheduleVersion++
		v.UpdatedAt = time.Now().UTC()
		v.NextCheckAt = v.UpdatedAt
		v.LastOutcome = "PENDING"
		return saveWatch(ctx, tx, v)
	})
	return v, err
}
func deleteWatchTx(ctx context.Context, tx *sql.Tx, user, id string) error {
	r, err := tx.ExecContext(ctx, "DELETE FROM watch_targets WHERE id=? AND user_id=?", id, user)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}
func (s *Store) DeleteWatch(ctx context.Context, user, id string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error { return deleteWatchTx(ctx, tx, user, id) })
}
func WatchTask(v d.WatchTarget) Task {
	t := NewTask("WATCH_CHECK", v.ID)
	t.WatchID = v.ID
	t.ScheduleVersion = v.ScheduleVersion
	t.ID = d.Hash(fmt.Sprintf("watch:%s:%d", v.ID, v.ScheduleVersion))[:32]
	return t
}
func (s *Store) ScheduleWatches(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, ErrValidation
	}
	count := 0
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		watches, err := Many[d.WatchTarget](ctx, tx, "SELECT body FROM watch_targets WHERE enabled=TRUE AND next_check_at<=? ORDER BY next_check_at,id LIMIT ? FOR UPDATE SKIP LOCKED", now, limit)
		if err != nil {
			return err
		}
		for _, v := range watches {
			if !v.RoundStartedAt.IsZero() && (v.LastOutcome == "QUEUED" || v.LastOutcome == "PROCESSING") {
				if now.Before(v.RoundStartedAt.Add(time.Hour)) {
					v.NextCheckAt = now.Add(5 * time.Minute)
					if err = saveWatch(ctx, tx, v); err != nil {
						return err
					}
					continue
				}
				v.RecordWatchRound(false)
			}
			interval, reason, err := watchInterval(ctx, tx, v, now)
			if err != nil {
				return err
			}
			v.ScheduleVersion++
			v.NextCheckAt = now.Add(time.Duration(interval) * time.Second)
			v.EffectiveInterval, v.ScheduleReason = interval, reason
			v.RoundChanged = false
			v.RoundStartedAt = now
			v.LastOutcome = "QUEUED"
			v.UpdatedAt = now
			if err = saveWatch(ctx, tx, v); err != nil {
				return err
			}
			if err = Outbox(ctx, tx, WatchTask(v)); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}
func loadWatchTask(ctx context.Context, q Queryer, t Task, lock string) (d.WatchTarget, error) {
	v, err := One[d.WatchTarget](ctx, q, "SELECT body FROM watch_targets WHERE id=?"+lock, t.WatchID)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrStaleWatch
	}
	if err != nil {
		return v, err
	}
	if (t.Type != "WATCH_CHECK" && t.Type != "WATCH_FETCH") || t.WatchID != t.EntityID || !v.Enabled || t.ScheduleVersion != v.ScheduleVersion || t.ID != watchIdentity(t) {
		return v, ErrStaleWatch
	}
	return v, nil
}
func (s *Store) WatchForTask(ctx context.Context, t Task) (d.WatchTarget, d.Source, error) {
	v, err := loadWatchTask(ctx, s.DB, t, "")
	if err != nil {
		return v, d.Source{}, err
	}
	src, err := watchSource(ctx, s.DB, v.UserID, v.SourceID)
	return v, src, err
}
func (s *Store) WatchInputs(ctx context.Context, id string) ([]Ingest, error) {
	return Many[Ingest](ctx, s.DB, "SELECT body FROM watch_postings WHERE watch_id=? ORDER BY posting_key LIMIT 501", id)
}
func (s *Store) WatchObservation(ctx context.Context, t Task, i Ingest) (d.Observation, error) {
	return One[d.Observation](ctx, s.DB, "SELECT o.body FROM watch_results r JOIN observations o ON o.id=r.observation_id WHERE r.watch_id=? AND r.schedule_version=? AND r.posting_key=? AND r.outcome='SUCCESS'", t.WatchID, t.ScheduleVersion, SourceKey(i))
}
func (s *Store) IngestWatch(ctx context.Context, t Task, i Ingest) (d.Observation, error) {
	var o d.Observation
	if err := i.Validate(); err != nil {
		return o, err
	}
	if i.ObservedAt.IsZero() {
		i.ObservedAt = time.Now().UTC()
	}
	if i.ObservedAt.After(time.Now().Add(time.Minute)) {
		return o, ErrValidation
	}
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		v, err := loadWatchTask(ctx, tx, t, " FOR UPDATE")
		if err != nil {
			return err
		}
		if i.SourceID != v.SourceID {
			return ErrValidation
		}
		key := SourceKey(i)
		outcome := "FAILURE"
		if i.FetchStatus == "SUCCESS" {
			outcome = "SUCCESS"
		}
		o, err = One[d.Observation](ctx, tx, "SELECT o.body FROM watch_results r JOIN observations o ON o.id=r.observation_id WHERE r.watch_id=? AND r.schedule_version=? AND r.posting_key=? AND (r.outcome=? OR r.outcome='SUCCESS') ORDER BY r.outcome DESC LIMIT 1", v.ID, t.ScheduleVersion, key, outcome)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		previous, previousErr := One[d.Observation](ctx, tx, "SELECT o.body FROM observations o JOIN postings p ON p.id=o.posting_id WHERE p.source_key=? AND JSON_UNQUOTE(JSON_EXTRACT(o.body,'$.fetch_status'))='SUCCESS' ORDER BY o.observed_at DESC,o.id DESC LIMIT 1", key)
		if previousErr != nil && !errors.Is(previousErr, sql.ErrNoRows) {
			return previousErr
		}
		o, err = s.ingestTx(ctx, tx, i)
		if err != nil {
			return err
		}
		if outcome == "SUCCESS" && (errors.Is(previousErr, sql.ErrNoRows) || previous.Hash != o.Hash) {
			v.RoundChanged = true
			if err = saveWatch(ctx, tx, v); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO watch_results(watch_id,schedule_version,posting_key,outcome,observation_id) VALUES(?,?,?,?,?)", v.ID, t.ScheduleVersion, key, outcome, o.ID); err != nil {
			return err
		}
		// Only posting references/metadata, never a second observation or assessment store.
		i.Text = ""
		i.FetchStatus = ""
		i.HTTPStatus = 0
		i.ObservedAt = time.Time{}
		_, err = tx.ExecContext(ctx, "INSERT INTO watch_postings(watch_id,posting_key,body) VALUES(?,?,?) ON DUPLICATE KEY UPDATE body=VALUES(body)", v.ID, key, d.JSON(i))
		return err
	})
	return o, err
}
func (s *Store) FinishWatch(ctx context.Context, t Task, outcome string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		v, err := loadWatchTask(ctx, tx, t, " FOR UPDATE")
		if errors.Is(err, ErrStaleWatch) {
			return nil
		}
		if err != nil {
			return err
		}
		if outcome == "FAILED" {
			_, err = tx.ExecContext(ctx, "INSERT INTO completed_tasks(id,completed_at) VALUES(?,?)", t.ID, time.Now().UTC())
			if duplicate(err) {
				return nil
			}
			if err != nil {
				return err
			}
		}
		now := time.Now().UTC()
		v.LastCheckedAt = &now
		v.LastOutcome = outcome
		v.UpdatedAt = now
		if outcome == "FAILED" {
			if err = finishWatchSchedule(ctx, tx, &v, now, false); err != nil {
				return err
			}
		}
		return saveWatch(ctx, tx, v)
	})
}

func watchIdentity(t Task) string {
	if t.Type == "WATCH_FETCH" && t.Posting != nil {
		return d.Hash(fmt.Sprintf("watch-fetch:%s:%d:%s", t.WatchID, t.ScheduleVersion, SourceKey(*t.Posting)))[:32]
	}
	return d.Hash(fmt.Sprintf("watch:%s:%d", t.WatchID, t.ScheduleVersion))[:32]
}
func (s *Store) WatchTaskDone(ctx context.Context, t Task) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM completed_tasks WHERE id=?", t.ID).Scan(&n)
	return n > 0, err
}

// Discovery fan-out is one SQL transaction. Fetches use the same existing Stream,
// each with its own bounded deadline and retry budget; no long network transaction.
func (s *Store) QueueWatchPostings(ctx context.Context, t Task, inputs []Ingest, discoveryFingerprint ...string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		v, err := loadWatchTask(ctx, tx, t, " FOR UPDATE")
		if err != nil {
			return err
		}
		var n int
		if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM completed_tasks WHERE id=?", t.ID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
		if len(discoveryFingerprint) > 1 {
			return ErrValidation
		}
		if len(discoveryFingerprint) == 1 {
			if len(discoveryFingerprint[0]) != 64 {
				return ErrValidation
			}
			v.RoundChanged = v.RoundChanged || v.DiscoveryHash != discoveryFingerprint[0]
			v.DiscoveryHash = discoveryFingerprint[0]
		}
		if len(inputs) > 500 {
			return ErrValidation
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO watch_runs(watch_id,schedule_version,expected_count) VALUES(?,?,?)", v.ID, t.ScheduleVersion, len(inputs))
		if err != nil {
			return err
		}
		for _, in := range inputs {
			if in.SourceID != v.SourceID || in.ExternalID == "" {
				return ErrValidation
			}
			in.Text = ""
			in.FetchStatus = ""
			in.ObservedAt = time.Time{}
			prior, e := One[Ingest](ctx, tx, "SELECT body FROM watch_postings WHERE watch_id=? AND posting_key=?", v.ID, SourceKey(in))
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if e != nil || prior.Title != in.Title || prior.JobType != in.JobType || d.JSON(prior.Locations) != d.JSON(in.Locations) {
				v.RoundChanged = true
			}
			child := WatchTask(v)
			child.Type = "WATCH_FETCH"
			child.Posting = &in
			child.ID = watchIdentity(child)
			child.CorrelationID = t.CorrelationID
			if err = Outbox(ctx, tx, child); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO completed_tasks(id,completed_at) VALUES(?,?)", t.ID, time.Now().UTC())
		if err != nil {
			return err
		}
		v.LastOutcome = "PROCESSING"
		if len(inputs) == 0 {
			now := time.Now().UTC()
			v.LastCheckedAt = &now
			v.LastOutcome = "SUCCESS"
			if err = finishWatchSchedule(ctx, tx, &v, now, true); err != nil {
				return err
			}
		}
		return saveWatch(ctx, tx, v)
	})
}
func (s *Store) CompleteWatchFetch(ctx context.Context, t Task, failed bool) (bool, error) {
	succeeded := false
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		v, err := loadWatchTask(ctx, tx, t, " FOR UPDATE")
		if errors.Is(err, ErrStaleWatch) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO completed_tasks(id,completed_at) VALUES(?,?)", t.ID, time.Now().UTC())
		if duplicate(err) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE watch_runs SET completed_count=completed_count+1,failed_count=failed_count+? WHERE watch_id=? AND schedule_version=?", failed, v.ID, t.ScheduleVersion)
		if err != nil {
			return err
		}
		var expected, complete, failures int
		if err = tx.QueryRowContext(ctx, "SELECT expected_count,completed_count,failed_count FROM watch_runs WHERE watch_id=? AND schedule_version=?", v.ID, t.ScheduleVersion).Scan(&expected, &complete, &failures); err != nil {
			return err
		}
		if expected == complete {
			now := time.Now().UTC()
			v.LastCheckedAt = &now
			v.LastOutcome = "SUCCESS"
			succeeded = failures == 0
			if failures > 0 {
				v.LastOutcome = "PARTIAL_FAILURE"
			}
			if err = finishWatchSchedule(ctx, tx, &v, now, failures == 0); err != nil {
				return err
			}
			return saveWatch(ctx, tx, v)
		}
		return nil
	})
	return succeeded, err
}

package persistence

import (
	"context"
	"database/sql"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"time"
)

func watchInterval(ctx context.Context, q Queryer, v d.WatchTarget, now time.Time) (int, string, error) {
	src, err := One[d.Source](ctx, q, "SELECT body FROM sources WHERE id=?", v.SourceID)
	if err != nil {
		return 0, "", err
	}
	minimum := d.SourceMinimumInterval(src.Adapter)
	urgent := false
	if v.Adaptive && v.FailureRounds == 0 && !v.Priority {
		// Inspect only this watch's tracked postings and the newest actual observation.
		rows, e := Many[d.Observation](ctx, q, `SELECT o.body FROM watch_postings w JOIN postings p ON p.source_key=w.posting_key JOIN observations o ON o.posting_id=p.id
   WHERE w.watch_id=? AND NOT EXISTS (SELECT 1 FROM observations n WHERE n.posting_id=o.posting_id AND (n.observed_at>o.observed_at OR (n.observed_at=o.observed_at AND n.id>o.id))) LIMIT 500`, v.ID)
		if e != nil {
			return 0, "", e
		}
		for _, o := range rows {
			if o.FetchStatus != "SUCCESS" || o.ObservedAt.Before(now.Add(-7*24*time.Hour)) {
				continue
			}
			deadline, e := d.DeadlineInstant(o.DeadlineSignal, o.Timezone)
			if e == nil && deadline.After(now) && !deadline.After(now.Add(72*time.Hour)) {
				urgent = true
				break
			}
		}
	}
	interval, reason := d.WatchInterval(v, minimum, urgent)
	return interval, reason, nil
}

func finishWatchSchedule(ctx context.Context, tx *sql.Tx, v *d.WatchTarget, now time.Time, success bool) error {
	v.RecordWatchRound(success)
	interval, reason, err := watchInterval(ctx, tx, *v, now)
	if err != nil {
		return err
	}
	v.EffectiveInterval, v.ScheduleReason = interval, reason
	v.NextCheckAt = now.Add(time.Duration(interval) * time.Second)
	return nil
}

// Cache stores only public adapter GET bodies, never browser model credentials.
type SourceHTTPEntry struct {
	ETag, LastModified string
	Body               []byte
}

func (s *Store) SourceHTTPRead(ctx context.Context, source, key string) (SourceHTTPEntry, error) {
	var v SourceHTTPEntry
	err := s.DB.QueryRowContext(ctx, "SELECT etag,last_modified,response FROM source_http_cache WHERE source_id=? AND request_key=? AND updated_at>UTC_TIMESTAMP(6)-INTERVAL 1 DAY", source, key).Scan(&v.ETag, &v.LastModified, &v.Body)
	return v, err
}
func (s *Store) SourceHTTPWrite(ctx context.Context, source, key string, v SourceHTTPEntry) error {
	if len(v.Body) > 1<<20 || len(v.ETag) > 512 || len(v.LastModified) > 128 {
		return ErrValidation
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var id string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM sources WHERE id=? FOR UPDATE", source).Scan(&id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO source_http_cache(source_id,request_key,etag,last_modified,response,updated_at) VALUES(?,?,?,?,?,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE etag=VALUES(etag),last_modified=VALUES(last_modified),response=VALUES(response),updated_at=VALUES(updated_at)`, source, key, v.ETag, v.LastModified, v.Body); err != nil {
			return err
		}
		var count, bytes int
		for {
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM(OCTET_LENGTH(response)),0) FROM source_http_cache WHERE source_id=?", source).Scan(&count, &bytes); err != nil {
				return err
			}
			if count <= 64 && bytes <= 16<<20 {
				return nil
			}
			r, err := tx.ExecContext(ctx, "DELETE FROM source_http_cache WHERE source_id=? ORDER BY updated_at,request_key LIMIT 1", source)
			if err != nil {
				return err
			}
			n, _ := r.RowsAffected()
			if n == 0 {
				return errors.New("source cache eviction failed")
			}
		}
	})
}

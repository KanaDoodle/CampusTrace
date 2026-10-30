package persistence

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/jobevent"
)

type JobEventPublisher interface {
	Publish(context.Context, []jobevent.Event) error
}

// A connection-owned lock orders publisher replicas. Remote I/O is outside a
// business transaction; only broker-acknowledged records are marked published.
func (s *Store) PublishJobEventsOnce(ctx context.Context, publisher JobEventPublisher) (int, error) {
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	var name string
	if err = conn.QueryRowContext(ctx, "SELECT CONCAT('ct-event:',LEFT(SHA2(DATABASE(),256),32))").Scan(&name); err != nil {
		return 0, err
	}
	var held int
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?,0)", name).Scan(&held); err != nil || held != 1 {
		return 0, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = conn.ExecContext(cleanup, "DO RELEASE_LOCK(?)", name)
	}()
	events, err := Many[jobevent.Event](ctx, s.DB, "SELECT body FROM job_event_outbox WHERE dataset=(SELECT dataset FROM search_revision_state WHERE id='jobs') AND published_at IS NULL ORDER BY event_version LIMIT 64")
	if err != nil || len(events) == 0 {
		return 0, err
	}
	send, cancel := context.WithTimeout(ctx, 6*time.Second)
	err = publisher.Publish(send, events)
	cancel()
	if err != nil {
		return 0, err
	}
	var owned int
	if err = conn.QueryRowContext(ctx, "SELECT IS_USED_LOCK(?)=CONNECTION_ID()", name).Scan(&owned); err != nil {
		return 0, err
	}
	if owned != 1 {
		return 0, ErrConflict
	}
	for _, event := range events {
		if _, err = conn.ExecContext(ctx, "UPDATE job_event_outbox SET published_at=UTC_TIMESTAMP(6) WHERE event_id=? AND published_at IS NULL", event.ID); err != nil {
			return 0, err
		}
	}
	if s.Metrics != nil {
		s.Metrics.Add("job_events_published_total", float64(len(events)))
	}
	return len(events), nil
}
func (s *Store) RunJobEventPublisher(ctx context.Context, publisher JobEventPublisher) {
	for ctx.Err() == nil {
		n, e := s.PublishJobEventsOnce(ctx, publisher)
		delay := 250 * time.Millisecond
		if n == 0 {
			delay = time.Second
		}
		if e != nil {
			delay = 2 * time.Second
			if s.Metrics != nil {
				s.Metrics.Add("job_event_publish_errors_total", 1)
			}
			slog.WarnContext(ctx, "job event publisher unavailable; durable records retained")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// ConsumeNotificationEvent uses Core truth and SQL receipts, never event text,
// for permission or message content. Fanout is bounded and fails closed.
func (s *Store) ConsumeNotificationEvent(ctx context.Context, event jobevent.Event) error {
	if !event.Valid() {
		return ErrValidation
	}
	if event.Kind == "snapshot" {
		return nil
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var dataset string
		if e := tx.QueryRowContext(ctx, "SELECT dataset FROM search_revision_state WHERE id='jobs'").Scan(&dataset); e != nil {
			return e
		}
		if dataset != event.Document.Dataset {
			return nil
		}
		result, e := tx.ExecContext(ctx, "INSERT IGNORE INTO job_notification_receipts(event_id,dataset,job_id,processed_at) VALUES(?,?,?,UTC_TIMESTAMP(6))", event.ID, dataset, event.Document.ID)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil || n == 0 {
			return e
		}
		if _, e = tx.ExecContext(ctx, "INSERT IGNORE INTO job_notification_versions(dataset,job_id,version) VALUES(?,?,0)", dataset, event.Document.ID); e != nil {
			return e
		}
		var version int64
		if e = tx.QueryRowContext(ctx, "SELECT version FROM job_notification_versions WHERE dataset=? AND job_id=? FOR UPDATE", dataset, event.Document.ID).Scan(&version); e != nil {
			return e
		}
		if version >= event.Document.Version {
			return nil
		}
		// Serialize ACL/document inspection with job mutations through commit.
		// The event-version row is consumer-owned; business writers never lock it.
		var lockedJob string
		e = tx.QueryRowContext(ctx, "SELECT id FROM jobs WHERE id=? FOR UPDATE", event.Document.ID).Scan(&lockedJob)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		current, e := One[struct {
			Version int64  `json:"version"`
			Hash    string `json:"hash"`
		}](ctx, tx, "SELECT body FROM search_documents WHERE job_id=?", event.Document.ID)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		// Delayed older changes do not generate stale notifications. The latest
		// genuine event remains independently consumable on its own partition key.
		if current.Version > event.Document.Version {
			return nil
		}
		if current.Version < event.Document.Version || current.Hash != event.Document.Hash {
			return ErrConflict
		}
		_, e = tx.ExecContext(ctx, "UPDATE job_notification_versions SET version=? WHERE dataset=? AND job_id=?", event.Document.Version, dataset, event.Document.ID)
		if e != nil {
			return e
		}
		if event.Document.Deleted {
			return nil
		}
		doc, e := searchDocument(ctx, tx, event.Document.ID, false)
		if e != nil {
			return e
		}
		if doc.Deleted {
			return nil
		}
		doc.Dataset = dataset
		if doc.ContentHash() != event.Document.Hash {
			return nil
		}
		rows, e := tx.QueryContext(ctx, `SELECT u.id FROM users u WHERE
   (?='PRIVATE' AND u.id=?) OR (?='GLOBAL' AND (
    EXISTS(SELECT 1 FROM user_job_preferences p WHERE p.user_id=u.id AND p.job_id=? AND p.disposition='SAVED') OR
    EXISTS(SELECT 1 FROM applications a WHERE a.user_id=u.id AND a.job_id=?) OR
    EXISTS(SELECT 1 FROM watch_targets w JOIN postings p ON p.source_id=w.source_id WHERE w.user_id=u.id AND p.job_id=? AND w.enabled=TRUE))) ORDER BY u.id LIMIT 1001`, doc.Visibility, doc.Owner, doc.Visibility, doc.ID, doc.ID, doc.ID)
		if e != nil {
			return e
		}
		users := []string{}
		for rows.Next() {
			var user string
			if e = rows.Scan(&user); e != nil {
				break
			}
			users = append(users, user)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return e
		}
		if len(users) > 1000 {
			return ErrRadarCapacity
		}
		typ, title := "JOB_CONTENT_CHANGED", "岗位资料更新"
		if event.Kind == "created" {
			typ, title = "NEW_JOB", "新增岗位资料"
		} else {
			for _, change := range event.Changes {
				if change == "status" {
					typ, title = "JOB_STATUS_CHANGED", "岗位状态变化"
				}
			}
		}
		for _, user := range users {
			if e = CheckJobAccess(ctx, tx, user, doc.ID); e != nil {
				if errors.Is(e, ErrNotFound) {
					continue
				}
				return e
			}
			var ignored int
			if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_job_preferences WHERE user_id=? AND job_id=? AND disposition='IGNORED'", user, doc.ID).Scan(&ignored); e != nil {
				return e
			}
			if ignored > 0 {
				continue
			}
			_, e = notification(ctx, tx, d.Notification{UserID: user, Type: typ, EntityType: "JOB", EntityID: doc.ID, EventVersion: "event:" + event.ID, Title: title, Body: doc.Company + " · " + doc.Title})
			if e != nil {
				return e
			}
		}
		// Acquire the epoch share lock only at commit, in job -> revision order.
		// If recovery rotated while this event was materializing, roll back every
		// side effect; retry will discard the now-foreign namespace at the entrance.
		var latest string
		if e = tx.QueryRowContext(ctx, "SELECT dataset FROM search_revision_state WHERE id='jobs' FOR SHARE").Scan(&latest); e != nil {
			return e
		}
		if latest != dataset {
			return ErrConflict
		}
		return nil
	})
}
func (s *Store) ConsumeNotificationEvents(ctx context.Context, events []jobevent.Event) error {
	for _, event := range events {
		if e := s.ConsumeNotificationEvent(ctx, event); e != nil {
			return e
		}
	}
	return nil
}

// ReplayPublishedJobEvents reopens a bounded history window. Event IDs and
// consumer receipts remain unchanged; it does not recreate business changes.
func (s *Store) ReplayPublishedJobEvents(ctx context.Context, after int64, limit int) (int, int64, error) {
	if after < 0 || limit < 1 || limit > 10000 {
		return 0, 0, ErrValidation
	}
	changed := 0
	var last int64
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		rows, e := tx.QueryContext(ctx, "SELECT event_id,event_version FROM job_event_outbox WHERE dataset=(SELECT dataset FROM search_revision_state WHERE id='jobs') AND event_version>? AND published_at IS NOT NULL ORDER BY event_version LIMIT ? FOR UPDATE", after, limit)
		if e != nil {
			return e
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id, &last); e != nil {
				break
			}
			ids = append(ids, id)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return e
		}
		for _, id := range ids {
			if _, e = tx.ExecContext(ctx, "UPDATE job_event_outbox SET published_at=NULL WHERE event_id=?", id); e != nil {
				return e
			}
			changed++
		}
		return nil
	})
	return changed, last, err
}

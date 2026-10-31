package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/jobevent"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	a "github.com/KanaDoodle/CampusTrace/internal/searchapi"
)

// Only derived job data crosses the service boundary. Authoritative ACL columns
// override the JSON copy; observations are selected deterministically.
func searchDocument(ctx context.Context, q Queryer, id string, lock bool) (a.Document, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	var raw []byte
	var visibility, owner string
	err := q.QueryRowContext(ctx, "SELECT body,visibility,owner_id FROM jobs WHERE id=?"+suffix, id).Scan(&raw, &visibility, &owner)
	doc := a.Document{ID: id, Deleted: errors.Is(err, sql.ErrNoRows)}
	if doc.Deleted {
		return doc, nil
	}
	if err != nil {
		return doc, err
	}
	var job d.Job
	if err = json.Unmarshal(raw, &job); err != nil {
		return doc, err
	}

	job.ID = id
	var observation []byte
	err = q.QueryRowContext(ctx, "SELECT body FROM observations WHERE job_id=? ORDER BY observed_at DESC,id DESC LIMIT 1", id).Scan(&observation)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return doc, err
	}
	if err == nil {
		var o d.Observation
		if err = json.Unmarshal(observation, &o); err != nil {
			return doc, err
		}
		doc.Text = o.Text
	}
	return projectSearchJob(job, visibility, owner, doc.Text), nil
}

func projectSearchJob(job d.Job, visibility, owner, text string) a.Document {
	doc := a.Document{ID: job.ID, Visibility: visibility, Owner: owner, Company: job.Company, Title: job.Title, JobType: job.JobType, Status: job.CurrentStatus, Text: text, Locations: make([]string, 0, len(job.Locations))}
	for _, location := range job.Locations {
		location = strings.ToValidUTF8(location, "")
		if len(location) > 256 {
			location = location[:256]
			for !utf8.ValidString(location) {
				location = location[:len(location)-1]
			}
		}
		doc.Locations = append(doc.Locations, location)
	}
	return doc
}

// Called inside the business transaction, with the job row locked first.
// Latest-state coalescing bounds backlog by number of jobs, not update count.
func (s *Store) touchSearchJob(ctx context.Context, tx *sql.Tx, id string) error {
	return s.writeSearchProjection(ctx, tx, id, s.JobEvents)
}
func (s *Store) writeSearchProjection(ctx context.Context, tx *sql.Tx, id string, events bool) error {
	doc, err := searchDocument(ctx, tx, id, true)
	if err != nil {
		return err
	}
	// Read namespace and revision under one lock. A writer which started before
	// recovery must not capture an old namespace and later use a new revision.
	if err = tx.QueryRowContext(ctx, "SELECT dataset,version FROM search_revision_state WHERE id='jobs' FOR UPDATE").Scan(&doc.Dataset, &doc.Version); err != nil {
		return err
	}
	doc.Hash = doc.ContentHash()
	var previous string
	var previousBody []byte
	err = tx.QueryRowContext(ctx, "SELECT content_hash,body FROM search_documents WHERE job_id=?", id).Scan(&previous, &previousBody)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && previous == doc.Hash {
		return nil
	}
	// The dataset/revision lock covers SQL only, never remote I/O.
	doc.Version++
	if events {
		var pending int
		if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM job_event_outbox WHERE dataset=? AND published_at IS NULL", doc.Dataset).Scan(&pending); err != nil {
			return err
		}
		if pending >= 100000 {
			return ErrQueueBusy
		}
	}
	if !doc.Valid() {
		return ErrValidation
	}
	if _, err = tx.ExecContext(ctx, "UPDATE search_revision_state SET version=? WHERE id='jobs'", doc.Version); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO search_documents(job_id,version,content_hash,body) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE version=VALUES(version),content_hash=VALUES(content_hash),body=VALUES(body)", id, doc.Version, doc.Hash, d.JSON(doc)); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO search_outbox(job_id,version,traceparent) VALUES(?,?,?) ON DUPLICATE KEY UPDATE version=VALUES(version),traceparent=VALUES(traceparent),created_at=CURRENT_TIMESTAMP(6)", id, doc.Version, observability.TraceParent(ctx))
	if err != nil {
		return err
	}

	// The revision lock serializes operation start/finish with business writes.
	// A coalesced delta never grows with the number of updates to the same job.
	if _, err = tx.ExecContext(ctx, `INSERT INTO search_rebuild_deltas(job_id,version)
 SELECT ?,? FROM search_rebuild_state WHERE id='jobs' AND dataset=? AND phase IN ('BACKFILL','VERIFY','CATCHUP')
 ON DUPLICATE KEY UPDATE version=VALUES(version)`, id, doc.Version, doc.Dataset); err != nil {
		return err
	}
	if events {
		var old *a.Document
		if len(previousBody) > 0 {
			var value a.Document
			if err = json.Unmarshal(previousBody, &value); err != nil {
				return err
			}
			old = &value
		}
		event := jobevent.New(doc, old, observability.TraceParent(ctx))
		if !event.Valid() {
			return ErrValidation
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO job_event_outbox(event_id,dataset,job_id,event_version,occurred_at,body) VALUES(?,?,?,?,?,?)", event.ID, doc.Dataset, doc.ID, doc.Version, event.OccurredAt, d.JSON(event))
	}
	return err
}

func (s *Store) reconcileSearch(ctx context.Context) error {
	var cursor string
	var initialDone bool
	var next sql.NullTime
	if err := s.DB.QueryRowContext(ctx, "SELECT reconcile_cursor,initial_done,next_reconcile_at FROM search_revision_state WHERE id='jobs'").Scan(&cursor, &initialDone, &next); err != nil {
		return err
	}
	if initialDone && next.Valid && time.Now().Before(next.Time) {
		return nil
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT id FROM jobs WHERE id>? ORDER BY id LIMIT 100", cursor)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = s.Tx(ctx, func(tx *sql.Tx) error { return s.writeSearchProjection(ctx, tx, id, s.JobEvents && initialDone) }); err != nil {
			return err
		}
	}
	if len(ids) < 100 {
		_, err = s.DB.ExecContext(ctx, "UPDATE search_revision_state SET reconcile_cursor='',initial_done=TRUE,next_reconcile_at=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 10 SECOND) WHERE id='jobs'")
	} else {
		_, err = s.DB.ExecContext(ctx, "UPDATE search_revision_state SET reconcile_cursor=?,next_reconcile_at=IF(initial_done,DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 10 SECOND),NULL) WHERE id='jobs'", ids[len(ids)-1])
	}
	if err != nil {
		return err
	}
	rows, err = s.DB.QueryContext(ctx, "SELECT p.job_id FROM search_documents p LEFT JOIN jobs j ON j.id=p.job_id WHERE j.id IS NULL AND JSON_EXTRACT(p.body,'$.deleted')=false LIMIT 100")
	if err != nil {
		return err
	}
	ids = nil
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = s.Tx(ctx, func(tx *sql.Tx) error { return s.writeSearchProjection(ctx, tx, id, s.JobEvents && initialDone) }); err != nil {
			return err
		}
	}
	return nil
}

// The connection-owned MySQL lock prevents competing worker replicas from
// interleaving rebuild cursors. Ownership is checked again before acknowledgement.
func (s *Store) SyncSearchOnce(ctx context.Context) error {
	if e := s.syncSearchForeground(ctx); e != nil {
		return e
	}
	s.observeOnlineRebuild(ctx, s.SyncSearchRebuildOnce(ctx))
	return nil
}
func (s *Store) syncSearchForeground(ctx context.Context) (err error) {
	if s.Search == nil {
		return nil
	}
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var name string
	if err = conn.QueryRowContext(ctx, "SELECT CONCAT('ct-search-sync:',LEFT(SHA2(DATABASE(),256),32))").Scan(&name); err != nil {
		return err
	}
	var held int
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?,0)", name).Scan(&held); err != nil {
		return err
	}
	if held != 1 {
		return nil
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = conn.ExecContext(release, "DO RELEASE_LOCK(?)", name)
	}()
	owned := func() error {
		var yes int
		if e := conn.QueryRowContext(ctx, "SELECT IS_USED_LOCK(?)=CONNECTION_ID()", name).Scan(&yes); e != nil {
			return e
		}
		if yes != 1 {
			return a.ErrConflict
		}
		return nil
	}
	status, err := s.Search.Status(ctx)
	if err != nil {
		return err
	}

	if err = s.reconcileSearch(ctx); err != nil {
		return err
	}
	var generation, cursor string
	var ready bool
	if err = s.DB.QueryRowContext(ctx, "SELECT generation,ready,bootstrap_cursor FROM search_sync_state WHERE id='jobs'").Scan(&generation, &ready, &cursor); err != nil {
		return err
	}
	if generation != status.Generation && !s.rebuildHandoff(ctx, status, generation) {
		if err = owned(); err != nil {
			return err
		}
		_, err = conn.ExecContext(ctx, "UPDATE search_sync_state SET generation=?,ready=FALSE,bootstrap_cursor='' WHERE id='jobs'", status.Generation)
		if err != nil {
			return err
		}
		generation, cursor, ready = status.Generation, "", false
	}
	if !ready {
		docs, e := Many[a.Document](ctx, s.DB, "SELECT body FROM search_documents WHERE job_id>? ORDER BY job_id LIMIT 64", cursor)
		if e != nil {
			return e
		}
		if len(docs) > 0 {
			if e = s.Search.Apply(ctx, a.Batch{Protocol: a.Protocol, Generation: generation, Documents: docs}); e != nil {
				return e
			}
			if e = owned(); e != nil {
				return e
			}
			_, e = conn.ExecContext(ctx, "UPDATE search_sync_state SET bootstrap_cursor=? WHERE id='jobs' AND generation=?", docs[len(docs)-1].ID, generation)
			return e
		}
		if err = owned(); err != nil {
			return err
		}
		if _, err = conn.ExecContext(ctx, "UPDATE search_sync_state SET ready=TRUE WHERE id='jobs' AND generation=?", generation); err != nil {
			return err
		}
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT p.body,o.traceparent FROM search_outbox o JOIN search_documents p ON p.job_id=o.job_id AND p.version=o.version ORDER BY o.version LIMIT 64")
	if err != nil {
		return err
	}
	docs := []a.Document{}
	parent := ""
	for rows.Next() {
		var raw []byte
		var p string
		if err = rows.Scan(&raw, &p); err != nil {
			break
		}
		var doc a.Document
		if err = json.Unmarshal(raw, &doc); err != nil {
			break
		}
		docs = append(docs, doc)
		if parent == "" {
			parent = p
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		return nil
	}
	// One coalesced batch follows the first item's trace; it contains no payload attributes.
	send, span := observability.Consumer(ctx, "search.outbox.publish", parent)
	err = s.Search.Apply(send, a.Batch{Protocol: a.Protocol, Generation: generation, Documents: docs})
	observability.End(span, err)
	if err != nil {
		return err
	}
	if err = owned(); err != nil {
		return err
	}
	for _, doc := range docs {
		if _, err = conn.ExecContext(ctx, "DELETE FROM search_outbox WHERE job_id=? AND version=?", doc.ID, doc.Version); err != nil {
			return err
		}
	}
	if s.Metrics != nil {
		s.Metrics.Add("search_documents_synced_total", float64(len(docs)))
	}
	return nil
}
func (s *Store) RunSearchSync(ctx context.Context) {
	var rebuild sync.WaitGroup
	rebuild.Add(1)
	go func() { defer rebuild.Done(); s.runOnlineRebuild(ctx) }()
	defer rebuild.Wait()
	for ctx.Err() == nil {
		err := s.syncSearchForeground(ctx)
		delay := 250 * time.Millisecond
		if err != nil && !errors.Is(err, a.ErrPending) {
			delay = 3 * time.Second
			if s.Metrics != nil {
				s.Metrics.Add("search_sync_errors_total", 1)
			}
			slog.WarnContext(ctx, "search synchronization unavailable; retrying")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

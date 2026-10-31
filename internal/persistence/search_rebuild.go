package persistence

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	a "github.com/KanaDoodle/CampusTrace/internal/searchapi"
)

func (s *Store) observeOnlineRebuild(ctx context.Context, e error) {
	if e == nil || errors.Is(e, a.ErrPending) || ctx.Err() != nil {
		return
	}
	if s.Metrics != nil {
		s.Metrics.Add("search_rebuild_errors_total", 1)
	}
	slog.WarnContext(ctx, "online Search rebuild deferred; active index retained")
}
func (s *Store) runOnlineRebuild(ctx context.Context) {
	for ctx.Err() == nil {
		e := s.SyncSearchRebuildOnce(ctx)
		s.observeOnlineRebuild(ctx, e)
		delay := 250 * time.Millisecond
		if e != nil && !errors.Is(e, a.ErrPending) {
			delay = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// This lock is independent of the foreground sync lock. Slow candidate writes
// cannot hold up normal index updates. Both are acquired only at cutover.
func (s *Store) searchRebuildLease(ctx context.Context, prefix string) (func(), func() error, error) {
	conn, e := s.DB.Conn(ctx)
	if e != nil {
		return nil, nil, e
	}
	var name string
	if e = conn.QueryRowContext(ctx, "SELECT CONCAT(?,LEFT(SHA2(DATABASE(),256),32))", prefix).Scan(&name); e != nil {
		conn.Close()
		return nil, nil, e
	}
	var held int
	if e = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?,0)", name).Scan(&held); e != nil || held != 1 {
		conn.Close()
		if e == nil {
			e = a.ErrPending
		}
		return nil, nil, e
	}
	release := func() {
		clean, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = conn.ExecContext(clean, "DO RELEASE_LOCK(?)", name)
		conn.Close()
	}
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
	return release, owned, nil
}
func (s *Store) SyncSearchRebuildOnce(ctx context.Context) error {
	if s.Search == nil {
		return nil
	}
	release, owned, e := s.searchRebuildLease(ctx, "ct-search-build:")
	if e != nil {
		return e
	}
	defer release()
	return s.syncOnlineRebuild(ctx, owned)
}

// One row is protected by the independent connection-owned rebuild lock. Job writers
// append coalesced deltas under the revision lock, in their business transaction.
type SearchRebuildState struct {
	Operation      string `json:"operation"`
	Dataset        string `json:"dataset"`
	Source         string `json:"source_generation"`
	Target         string `json:"target_generation"`
	Phase          string `json:"phase"`
	Cursor         string `json:"bootstrap_cursor"`
	Copied         int64  `json:"copied"`
	Verified       int64  `json:"verified"`
	StartedVersion int64  `json:"started_version"`
	CutoverVersion int64  `json:"cutover_version"`
}

func (s *Store) SearchRebuildState(ctx context.Context) (r SearchRebuildState, err error) {
	err = s.DB.QueryRowContext(ctx, "SELECT operation,dataset,source_generation,target_generation,phase,bootstrap_cursor,copied,verified,started_version,cutover_version FROM search_rebuild_state WHERE id='jobs'").Scan(&r.Operation, &r.Dataset, &r.Source, &r.Target, &r.Phase, &r.Cursor, &r.Copied, &r.Verified, &r.StartedVersion, &r.CutoverVersion)
	return
}
func (s *Store) rebuildHandoff(ctx context.Context, status a.Status, generation string) bool {
	p := status.Rebuild
	if p == nil || p.Source != generation || p.Target != status.Generation || p.Phase != "CUTTING" && p.Phase != "COMMITTED" {
		return false
	}
	r, e := s.SearchRebuildState(ctx)
	if e != nil || r.Operation != p.Operation || r.Dataset != p.Dataset || r.Source != generation || r.Target != p.Target || r.Phase != "CATCHUP" {
		return false
	}
	var dataset string
	if s.DB.QueryRowContext(ctx, "SELECT dataset FROM search_revision_state WHERE id='jobs'").Scan(&dataset) != nil {
		return false
	}
	return dataset == r.Dataset
}
func (s *Store) syncOnlineRebuild(ctx context.Context, owned func() error) error {
	transport, ok := s.Search.(a.OnlineRebuilder)
	if !ok {
		return nil
	}
	status, e := s.Search.Status(ctx)
	if e != nil {
		return e
	}
	p := status.Rebuild
	w, e := readSearchWatermark(ctx, s.DB)
	if e != nil {
		return e
	}
	r, e := s.SearchRebuildState(ctx)
	if e != nil {
		return e
	}
	if p == nil {
		if r.Phase == "BACKFILL" || r.Phase == "VERIFY" || r.Phase == "CATCHUP" {
			return s.abandonSearchRebuild(ctx, r)
		}
		return nil
	}
	if p.Phase == "ABORTED" {
		if r.Operation == p.Operation && r.Phase != "ABORTED" {
			return s.abandonSearchRebuild(ctx, r)
		}
		return nil
	}
	if p.Phase == "DONE" {
		return nil
	}
	if p.Dataset != "" && p.Dataset != w.dataset {
		return nil
	} // Old restored timeline is never resumed.
	if r.Operation == p.Operation && r.Phase == "DONE" && w.generation == p.Target {
		_, e = transport.AckRebuild(ctx, a.RebuildRef{Operation: r.Operation, Dataset: r.Dataset})
		return e
	}
	if !w.usable() {
		return nil
	}
	if p.Target == "" {
		return a.ErrPending
	}
	if r.Operation != p.Operation {
		if p.Source != w.generation || status.Generation != w.generation || p.Phase != "BUILDING" {
			return a.ErrConflict
		}
		if _, e = transport.ClaimRebuild(ctx, a.RebuildRef{Operation: p.Operation, Dataset: w.dataset}); e != nil {
			return e
		}
		if e = owned(); e != nil {
			return e
		}
		return s.Tx(ctx, func(tx *sql.Tx) error {
			var dataset, generation string
			var version int64
			if e := tx.QueryRowContext(ctx, "SELECT dataset,version FROM search_revision_state WHERE id='jobs' FOR UPDATE").Scan(&dataset, &version); e != nil {
				return e
			}
			if e := tx.QueryRowContext(ctx, "SELECT generation FROM search_sync_state WHERE id='jobs' FOR SHARE").Scan(&generation); e != nil {
				return e
			}
			if dataset != w.dataset || generation != p.Source {
				return a.ErrConflict
			}

			var previous string
			if e := tx.QueryRowContext(ctx, "SELECT operation FROM search_rebuild_state WHERE id='jobs' FOR UPDATE").Scan(&previous); e != nil {
				return e
			}
			if previous != r.Operation {
				return a.ErrConflict
			}
			if e := owned(); e != nil {
				return e
			}

			if _, e := tx.ExecContext(ctx, "DELETE FROM search_rebuild_deltas"); e != nil {
				return e
			}
			_, e := tx.ExecContext(ctx, `UPDATE search_rebuild_state SET operation=?,dataset=?,source_generation=?,target_generation=?,phase='BACKFILL',bootstrap_cursor='',copied=0,verified=0,started_version=?,cutover_version=0,updated_at=UTC_TIMESTAMP(6) WHERE id='jobs'`, p.Operation, dataset, p.Source, p.Target, version)
			return e
		})
	}
	if status.Generation != r.Source && status.Generation != r.Target {
		return a.ErrConflict
	}
	if r.Dataset != w.dataset || r.Source != p.Source || r.Target != p.Target {
		return a.ErrConflict
	}
	if r.Phase == "BACKFILL" {
		docs, e := Many[a.Document](ctx, s.DB, "SELECT body FROM search_documents WHERE job_id>? ORDER BY job_id LIMIT 64", r.Cursor)
		if e != nil {
			return e
		}
		if len(docs) > 0 {
			if e = s.Search.Apply(ctx, a.Batch{Protocol: a.Protocol, Generation: r.Target, Documents: docs}); e != nil {
				return e
			}
			if e = owned(); e != nil {
				return e
			}
			_, e = s.DB.ExecContext(ctx, "UPDATE search_rebuild_state SET bootstrap_cursor=?,copied=copied+?,updated_at=UTC_TIMESTAMP(6) WHERE id='jobs' AND operation=? AND dataset=? AND phase='BACKFILL'", docs[len(docs)-1].ID, len(docs), r.Operation, r.Dataset)
			return e
		}
		if e = owned(); e != nil {
			return e
		}
		_, e = s.DB.ExecContext(ctx, "UPDATE search_rebuild_state SET phase='VERIFY',bootstrap_cursor='',updated_at=UTC_TIMESTAMP(6) WHERE id='jobs' AND operation=? AND dataset=? AND phase='BACKFILL'", r.Operation, r.Dataset)
		return e
	}

	if r.Phase == "VERIFY" {
		docs, e := Many[a.Document](ctx, s.DB, "SELECT body FROM search_documents WHERE job_id>? ORDER BY job_id LIMIT 64", r.Cursor)
		if e != nil {
			return e
		}
		if len(docs) == 0 {
			if e = owned(); e != nil {
				return e
			}
			_, e = s.DB.ExecContext(ctx, "UPDATE search_rebuild_state SET phase='CATCHUP',bootstrap_cursor='',updated_at=UTC_TIMESTAMP(6) WHERE id='jobs' AND operation=? AND dataset=? AND phase='VERIFY'", r.Operation, r.Dataset)
			return e
		}
		visible, ok := s.Search.(interface {
			Applied(context.Context, a.AppliedQuery) (a.AppliedResult, error)
		})
		if !ok {
			return a.ErrUnavailable
		}
		hits := make([]a.Hit, 0, len(docs))
		for _, doc := range docs {
			hits = append(hits, a.Hit{ID: doc.ID, Version: doc.Version, Hash: doc.Hash})
		}
		ack, e := visible.Applied(ctx, a.AppliedQuery{Protocol: a.Protocol, Generation: r.Target, Dataset: r.Dataset, Hits: hits, Exact: true})
		if e != nil {
			return e
		}
		if !ack.Ready {
			// Repair a missing/older copy, then verify the same cursor on the next turn.
			// Equal-version different hashes fail closed in the normal Apply contract.
			if e = s.Search.Apply(ctx, a.Batch{Protocol: a.Protocol, Generation: r.Target, Documents: docs}); e != nil {
				return e
			}
			return a.ErrPending
		}
		if e = owned(); e != nil {
			return e
		}
		_, e = s.DB.ExecContext(ctx, "UPDATE search_rebuild_state SET bootstrap_cursor=?,verified=verified+?,updated_at=UTC_TIMESTAMP(6) WHERE id='jobs' AND operation=? AND dataset=? AND phase='VERIFY'", docs[len(docs)-1].ID, len(docs), r.Operation, r.Dataset)
		return e
	}
	if r.Phase != "CATCHUP" {
		return a.ErrConflict
	}
	docs, e := Many[a.Document](ctx, s.DB, "SELECT p.body FROM search_rebuild_deltas d JOIN search_documents p ON p.job_id=d.job_id AND p.version=d.version ORDER BY d.version LIMIT 64")
	if e != nil {
		return e
	}
	if len(docs) > 0 {
		if e = s.Search.Apply(ctx, a.Batch{Protocol: a.Protocol, Generation: r.Target, Documents: docs}); e != nil {
			return e
		}
		if e = owned(); e != nil {
			return e
		}
		for _, doc := range docs {
			if _, e = s.DB.ExecContext(ctx, "DELETE FROM search_rebuild_deltas WHERE job_id=? AND version=? AND EXISTS(SELECT 1 FROM search_rebuild_state WHERE id='jobs' AND operation=? AND dataset=? AND phase='CATCHUP')", doc.ID, doc.Version, r.Operation, r.Dataset); e != nil {
				return e
			}
		}
		return nil
	}
	return s.commitOnlineRebuild(ctx, transport, r, owned)
}

// This is the only short write barrier. Candidate I/O and full replay happen
// before it. MVCC readers continue reading the old, retained physical generation.
// A lost remote reply does not update SQL; the durable CUTTING plan is resumed.
func (s *Store) commitOnlineRebuild(parent context.Context, remote a.OnlineRebuilder, r SearchRebuildState, owned func() error) error {

	release, syncOwned, e := s.searchRebuildLease(parent, "ct-search-sync:")
	if e != nil {
		return e
	}
	defer release()
	ctx, cancel := context.WithTimeout(parent, 1200*time.Millisecond)
	defer cancel()
	started := time.Now()
	if s.Metrics != nil {
		defer func() { s.Metrics.Observe("search_rebuild_barrier_seconds", time.Since(started).Seconds()) }()
	}
	e = s.Tx(ctx, func(tx *sql.Tx) error {
		var dataset string
		var version int64
		if e := tx.QueryRowContext(ctx, "SELECT dataset,version FROM search_revision_state WHERE id='jobs' FOR UPDATE").Scan(&dataset, &version); e != nil {
			return e
		}
		if dataset != r.Dataset {
			return a.ErrConflict
		}
		var dirty bool
		if e := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM search_outbox LIMIT 1) OR EXISTS(SELECT 1 FROM search_rebuild_deltas LIMIT 1)").Scan(&dirty); e != nil {
			return e
		}
		if dirty {
			return a.ErrPending
		}
		var operation, phase, generation string
		if e := tx.QueryRowContext(ctx, "SELECT operation,phase FROM search_rebuild_state WHERE id='jobs'").Scan(&operation, &phase); e != nil {
			return e
		}
		if operation != r.Operation || phase != "CATCHUP" {
			return a.ErrConflict
		}
		if e := tx.QueryRowContext(ctx, "SELECT generation FROM search_sync_state WHERE id='jobs' FOR SHARE").Scan(&generation); e != nil {
			return e
		}
		if generation != r.Source {
			return a.ErrConflict
		}
		var count int64
		if e := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_documents").Scan(&count); e != nil {
			return e
		}
		if e := owned(); e != nil {
			return e
		}
		if e := syncOwned(); e != nil {
			return e
		}
		result, e := remote.CommitRebuild(ctx, a.RebuildCommit{RebuildRef: a.RebuildRef{Operation: r.Operation, Dataset: r.Dataset}, Version: version, Documents: count})
		if e != nil {
			return e
		}
		if result.Generation != r.Target || result.Rebuild == nil || result.Rebuild.Operation != r.Operation || result.Rebuild.Phase != "COMMITTED" {
			return a.ErrConflict
		}
		if e = owned(); e != nil {
			return e
		}
		if e = syncOwned(); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE search_sync_state SET generation=?,ready=TRUE,bootstrap_cursor='' WHERE id='jobs'", r.Target); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE search_rebuild_state SET phase='DONE',cutover_version=?,updated_at=UTC_TIMESTAMP(6) WHERE id='jobs' AND operation=?", version, r.Operation)
		return e
	})
	if e != nil {
		return e
	}
	if s.Metrics != nil {
		s.Metrics.Add("search_rebuild_cutovers_total", 1)
	}
	_, e = remote.AckRebuild(parent, a.RebuildRef{Operation: r.Operation, Dataset: r.Dataset})
	return e
}

func (s *Store) abandonSearchRebuild(ctx context.Context, r SearchRebuildState) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var dataset string
		if e := tx.QueryRowContext(ctx, "SELECT dataset FROM search_revision_state WHERE id='jobs' FOR UPDATE").Scan(&dataset); e != nil {
			return e
		}
		if dataset != r.Dataset {
			return a.ErrConflict
		}
		var operation, phase string
		if e := tx.QueryRowContext(ctx, "SELECT operation,phase FROM search_rebuild_state WHERE id='jobs' FOR UPDATE").Scan(&operation, &phase); e != nil {
			return e
		}
		if operation != r.Operation || phase != "BACKFILL" && phase != "VERIFY" && phase != "CATCHUP" {
			return a.ErrConflict
		}
		if _, e := tx.ExecContext(ctx, "DELETE FROM search_rebuild_deltas"); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, "UPDATE search_rebuild_state SET phase='ABORTED',updated_at=UTC_TIMESTAMP(6) WHERE id='jobs' AND operation=?", r.Operation)
		return e
	})
}

// Fixed operational dimensions only; no operation, dataset, job or user IDs.
func (s *Store) SearchRebuildStats(ctx context.Context) (phase int, copied, total, pending int64, err error) {
	r, e := s.SearchRebuildState(ctx)
	if e != nil {
		err = e
		return
	}
	phase = map[string]int{"IDLE": 0, "BACKFILL": 1, "VERIFY": 2, "CATCHUP": 3, "DONE": 4, "ABORTED": 5}[r.Phase]
	copied = r.Copied
	if r.Phase == "BACKFILL" || r.Phase == "VERIFY" || r.Phase == "CATCHUP" {
		err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_documents").Scan(&total)
		if err != nil {
			return
		}
	}
	err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_rebuild_deltas").Scan(&pending)
	return
}

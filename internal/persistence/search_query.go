package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	a "github.com/KanaDoodle/CampusTrace/internal/searchapi"
)

type JobSearch struct {
	Jobs []d.Job  `json:"jobs"`
	IDs  []string `json:"job_ids"`
	Mode string   `json:"mode"`
	More bool     `json:"more"`
}
type searchWatermark struct {
	dataset, generation   string
	version               int64
	initial, ready, dirty bool
}

func readSearchWatermark(ctx context.Context, q Queryer) (v searchWatermark, err error) {
	ctx, span := observability.Start(ctx, "search.watermark")
	defer func() { observability.End(span, err) }()
	err = q.QueryRowContext(ctx, "SELECT r.dataset,r.version,r.initial_done,s.generation,s.ready,EXISTS(SELECT 1 FROM search_outbox LIMIT 1) FROM search_revision_state r JOIN search_sync_state s ON s.id=r.id WHERE r.id='jobs'").Scan(&v.dataset, &v.version, &v.initial, &v.generation, &v.ready, &v.dirty)
	return
}
func (w searchWatermark) usable() bool { return w.initial && w.ready && !w.dirty && w.generation != "" }

// Search returns only data hydrated from Core in a consistent ACL snapshot.
// Any unacknowledged write, changed generation, missing hit or content mismatch
// causes the entire request to use the bounded authoritative fallback.
func (s *Store) SearchJobs(ctx context.Context, user string, q a.Query) (out JobSearch, err error) {
	ctx, span := observability.Start(ctx, "search.core")
	defer func() { observability.End(span, err) }()
	if !a.ID(user) {
		return JobSearch{}, ErrNotFound
	}
	if e := q.Normalize(); e != nil || q.Limit > 1000 {
		return JobSearch{}, ErrValidation
	}
	if e := ctx.Err(); e != nil {
		return JobSearch{}, e
	}
	if !s.searchReads.enter(false) {
		if s.Metrics != nil {
			s.Metrics.Add("search_read_rejected_total", 1)
		}
		return JobSearch{}, ErrSearchBusy
	}
	defer s.searchReads.leave(false)
	// Includes watermark SQL, remote query, hydration and fallback. Reserve the
	// caller's tighter deadline; never continue background DB work after timeout.
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, a.RequestBudget)
	defer cancel()
	defer func() {
		if err != nil && ctx.Err() != nil && parent.Err() == nil {
			err = ErrSearchBusy
			if s.Metrics != nil {
				s.Metrics.Add("search_read_timeout_total", 1)
			}
		}
	}()
	reason := "disabled"
	if s.Search != nil {
		before, e := readSearchWatermark(ctx, s.DB)
		if e != nil {
			return JobSearch{}, errors.Join(ErrBackendUnavailable, e)
		}
		reason = "pending"
		if before.usable() {
			q.User, q.Dataset, q.Generation = user, before.dataset, before.generation
			requested := q.Limit
			remoteQuery := q
			remoteQuery.Limit = q.Limit + 1
			result, e := s.Search.Query(ctx, remoteQuery)
			reason = "unavailable"
			if ctx.Err() != nil {
				return JobSearch{}, ctx.Err()
			}
			switch {
			case errors.Is(e, a.ErrCircuitOpen):
				reason = "circuit"
			case errors.Is(e, a.ErrQueryBusy):
				reason = "remote_busy"
			case errors.Is(e, a.ErrQueryBudget):
				reason = "budget"
			case errors.Is(e, a.ErrConflict):
				reason = "snapshot"
			}
			if e == nil {
				out, e := s.hydrateSearch(ctx, user, before, result)
				if e == nil {
					if len(out.IDs) > requested {
						out.More = true
						out.IDs = out.IDs[:requested]
						out.Jobs = out.Jobs[:requested]
					}
					if s.Metrics != nil {
						s.Metrics.Add("search_fulltext_total", 1)
					}
					return out, nil
				}
				if !errors.Is(e, a.ErrConflict) {
					return JobSearch{}, errors.Join(ErrBackendUnavailable, e)
				}
				reason = "snapshot"
			}
		}
	}
	s.recordSearchFallback(reason)
	return s.guardedBasicSearch(ctx, user, q)
}

func (s *Store) hydrateSearch(ctx context.Context, user string, before searchWatermark, result a.Result) (JobSearch, error) {
	ctx, span := observability.Start(ctx, "search.hydrate")
	defer span.End()
	out := JobSearch{Jobs: []d.Job{}, IDs: []string{}, Mode: "fulltext", More: false}
	if result.Protocol != a.Protocol || result.Generation != before.generation || len(result.Hits) > a.MaxHits {
		return out, a.ErrConflict
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	after, err := readSearchWatermark(ctx, tx)
	if err != nil || !sameSearchReadSnapshot(ctx, tx, before, after) {
		return out, a.ErrConflict
	}
	seen := map[string]bool{}
	for _, hit := range result.Hits {
		if !a.ID(hit.ID) || seen[hit.ID] {
			return out, a.ErrConflict
		}
		seen[hit.ID] = true
	}
	jobs := map[string]d.Job{}
	versions := map[string]int64{}
	hashes := map[string]string{}
	for offset := 0; offset < len(result.Hits); offset += 100 {
		end := offset + 100
		if end > len(result.Hits) {
			end = len(result.Hits)
		}
		args := []any{}
		for _, hit := range result.Hits[offset:end] {
			args = append(args, hit.ID)
		}
		args = append(args, user)
		placeholders := strings.TrimSuffix(strings.Repeat("?,", end-offset), ",")
		rows, e := tx.QueryContext(ctx, `SELECT j.id,j.body,j.visibility,j.owner_id,p.version,
   COALESCE((SELECT JSON_UNQUOTE(JSON_EXTRACT(o.body,'$.text')) FROM observations o WHERE o.job_id=j.id ORDER BY o.observed_at DESC,o.id DESC LIMIT 1),'')
   FROM jobs j JOIN search_documents p ON p.job_id=j.id WHERE j.id IN (`+placeholders+`) AND (j.visibility='GLOBAL' OR (j.visibility='PRIVATE' AND j.owner_id=?))`, args...)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var raw []byte
			var id, visibility, owner, text string
			var version int64
			if e = rows.Scan(&id, &raw, &visibility, &owner, &version, &text); e != nil {
				break
			}
			var job d.Job
			if e = json.Unmarshal(raw, &job); e != nil {
				break
			}
			job.ID = id
			doc := projectSearchJob(job, visibility, owner, text)
			doc.Dataset = before.dataset
			job.Visibility, job.OwnerID = visibility, owner
			jobs[id] = job
			versions[id] = version
			hashes[id] = doc.ContentHash()
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return out, e
		}
	}
	for _, hit := range result.Hits {
		job, ok := jobs[hit.ID]
		if !ok || versions[hit.ID] != hit.Version || hashes[hit.ID] != hit.Hash {
			return out, a.ErrConflict
		}
		out.Jobs = append(out.Jobs, job)
		out.IDs = append(out.IDs, hit.ID)
	}

	return out, tx.Commit()
}
func (s *Store) basicSearch(ctx context.Context, user string, q a.Query) (JobSearch, error) {
	ctx, span := observability.Start(ctx, "search.fallback")
	defer span.End()
	out := JobSearch{Jobs: []d.Job{}, IDs: []string{}, Mode: "basic"}
	cursor := ""
	matcher := q.PrepareMatcher()
	// The fallback is deliberately bounded. More also signals an incomplete scan.
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	for scanned := 0; scanned < 10000; {
		// Keep common queries cheap; amortize round trips only after a full
		// first page. The authoritative scan still reads at most 10,000 rows.
		batchSize := 100
		if scanned > 0 {
			batchSize = 500
		}
		if left := 10000 - scanned; batchSize > left {
			batchSize = left
		}
		pageCtx, page := observability.Start(ctx, "search.fallback.page")
		rows, e := tx.QueryContext(pageCtx, `SELECT j.id,j.body,j.visibility,j.owner_id,
   COALESCE((SELECT JSON_UNQUOTE(JSON_EXTRACT(o.body,'$.text')) FROM observations o WHERE o.job_id=j.id ORDER BY o.observed_at DESC,o.id DESC LIMIT 1),'')
   FROM jobs j WHERE j.id>? AND (j.visibility='GLOBAL' OR (j.visibility='PRIVATE' AND j.owner_id=?)) ORDER BY j.id LIMIT ?`, cursor, user, batchSize)
		if e != nil {
			page.End()
			return out, e
		}
		count := 0
		for rows.Next() {
			// Unmarshal consumes this row before Next invalidates RawBytes.
			var raw sql.RawBytes
			var id, visibility, owner, text string
			if e = rows.Scan(&id, &raw, &visibility, &owner, &text); e != nil {
				break
			}
			count++
			cursor = id
			var job d.Job
			if e = json.Unmarshal(raw, &job); e != nil {
				break
			}
			doc := a.Document{ID: id, Company: job.Company, Title: job.Title, Locations: job.Locations, JobType: job.JobType, Text: text}
			if matcher.Matches(doc) {
				if len(out.Jobs) == q.Limit {
					out.More = true
					break
				}
				job.ID, job.OwnerID, job.Visibility = id, owner, visibility
				out.Jobs = append(out.Jobs, job)
				out.IDs = append(out.IDs, id)
			}
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		page.End()
		if e != nil {
			return out, e
		}
		scanned += count
		if out.More || count < batchSize {
			return out, tx.Commit()
		}
	}
	out.More = true
	return out, tx.Commit()
}

// SearchSyncStats is bounded operational state; it exposes no job or user IDs.
func (s *Store) SearchSyncStats(ctx context.Context) (pending int64, ready bool, err error) {
	w, e := readSearchWatermark(ctx, s.DB)
	if e != nil {
		return 0, false, e
	}
	err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_outbox").Scan(&pending)
	return pending, w.usable(), err
}

// A completed online handoff preserves the dataset and authoritative revision.
// In-flight readers may finish against its retained source. A real data/ACL
// mutation still invalidates the read and uses the authoritative fallback.
func sameSearchReadSnapshot(ctx context.Context, q Queryer, before, after searchWatermark) bool {
	if before == after {
		return true
	}
	left, right := before, after
	left.generation = ""
	right.generation = ""
	if left != right || !before.usable() || !after.usable() {
		return false
	}
	var matching bool
	e := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM search_rebuild_state WHERE id='jobs' AND phase='DONE' AND dataset=? AND source_generation=? AND target_generation=? AND cutover_version=?)`, before.dataset, before.generation, after.generation, before.version).Scan(&matching)
	return e == nil && matching
}

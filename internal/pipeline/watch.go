package pipeline

import (
	"context"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/source"
	"log/slog"
	"sort"
	"strings"
	"time"
)

func (w *Worker) sourceAdapter() source.DiscoveryAdapter {
	if w.Source != nil {
		return w.Source
	}
	return source.PublicPlatform{CacheRead: func(ctx context.Context, id, key string) (source.HTTPEntry, error) {
		v, e := w.Store.SourceHTTPRead(ctx, id, key)
		return source.HTTPEntry{ETag: v.ETag, LastModified: v.LastModified, Body: v.Body}, e
	}, CacheWrite: func(ctx context.Context, id, key string, v source.HTTPEntry) error {
		return w.Store.SourceHTTPWrite(ctx, id, key, p.SourceHTTPEntry{ETag: v.ETag, LastModified: v.LastModified, Body: v.Body})
	}, Allow: func(ctx context.Context, id string, n int) (bool, error) {
		return w.Queue.Allow(ctx, "source:"+id, n, time.Minute)
	}}
}
func (w *Worker) processWatch(ctx context.Context, t p.Task) error {
	watch, src, err := w.Store.WatchForTask(ctx, t)
	if errors.Is(err, p.ErrStaleWatch) {
		return nil
	}
	if err != nil {
		return err
	}
	done, err := w.Store.WatchTaskDone(ctx, t)
	if err != nil || done {
		return err
	}
	adapter := w.sourceAdapter()
	if t.Type == "WATCH_CHECK" {
		refs, err := adapter.Discover(ctx, src, watch)
		if err != nil {
			var fetch *source.FetchError
			if source.AsFetchError(err, &fetch) && fetch.Category == "RATE_LIMIT" {
				return err
			}
			w.Metrics.Add("watch_checks_failed", 1)
			_ = w.Store.FinishWatch(ctx, t, "DISCOVERY_FAILED")
			return err
		}
		prior, err := w.Store.WatchInputs(ctx, watch.ID)
		if err != nil {
			return err
		}
		inputs := map[string]p.Ingest{}
		for _, in := range prior {
			if watch.Keyword == "" || strings.Contains(strings.ToLower(in.Title+" "+strings.Join(in.Locations, " ")), strings.ToLower(watch.Keyword)) {
				inputs[p.SourceKey(in)] = in
			}
		}
		for _, r := range refs {
			in := p.Ingest{SourceID: src.ID, Company: r.Company, Title: r.Title, JobType: r.JobType, Locations: r.Locations, ExternalID: r.ExternalID, URL: r.URL}
			inputs[p.SourceKey(in)] = in
		}
		if len(inputs) > source.MaxPostings {
			return &source.FetchError{Category: "CAPACITY"}
		}
		keys := []string{}
		for k := range inputs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		rows := []p.Ingest{}
		for _, k := range keys {
			rows = append(rows, inputs[k])
		}
		// A disappearance changes scheduling urgency, never proves closure.
		fingerprints := make([]string, 0, len(refs))
		for _, ref := range refs {
			locations := append([]string(nil), ref.Locations...)
			sort.Strings(locations)
			ref.Locations = locations
			fingerprints = append(fingerprints, d.JSON(ref))
		}
		sort.Strings(fingerprints)
		err = w.Store.QueueWatchPostings(ctx, t, rows, d.Hash(d.JSON(fingerprints)))
		if errors.Is(err, p.ErrStaleWatch) {
			return nil
		}
		if err == nil && len(rows) == 0 {
			w.Metrics.Add("watch_checks_succeeded", 1)
		}
		return err
	}
	in := *t.Posting
	if in.SourceID != src.ID {
		return p.ErrValidation
	}
	// If a previous delivery committed the observation but crashed before ACK,
	// skip the network and finish the remaining SQL receipt.
	if _, err = w.Store.WatchObservation(ctx, t, in); err == nil {
		return w.completeWatchFetch(ctx, t, false)
	} else if !errors.Is(err, p.ErrNotFound) {
		return err
	}
	start := time.Now()
	snapshot, fetchErr := adapter.FetchPosting(ctx, src, source.PostingRef{ExternalID: in.ExternalID, URL: in.URL, Title: in.Title, Company: in.Company, JobType: in.JobType, Locations: in.Locations})
	w.Metrics.Since("source_fetch_latency", start)
	if snapshot.Status != "" {
		if versioned, ok := adapter.(interface{ Version() string }); ok {
			in.SourceParserVersion = versioned.Version()
		}
		in.Text, in.FetchStatus, in.HTTPStatus, in.ObservedAt = snapshot.Text, snapshot.Status, snapshot.HTTPStatus, time.Now().UTC()
		o, err := w.Store.IngestWatch(ctx, t, in)
		if errors.Is(err, p.ErrStaleWatch) {
			return nil
		}
		if err != nil {
			return err
		}
		slog.InfoContext(ctx, "watch posting observed", "watch_id", watch.ID, "source_id", src.ID, "job_id", o.JobID, "task_id", t.ID, "correlation_id", t.CorrelationID)
		// First posting observation is an actual new source discovery, not a fetch count.
		posting, err := p.One[d.Posting](ctx, w.Store.DB, "SELECT body FROM postings WHERE id=?", o.PostingID)
		if err != nil {
			return err
		}
		if posting.FirstSeen.Equal(o.ObservedAt) && o.ObservedAt.Equal(in.ObservedAt) {
			j, err := w.Store.Job(ctx, o.JobID)
			if err != nil {
				return err
			}
			if j.CreatedAt.Equal(o.ObservedAt) {
				w.Metrics.Add("new_jobs_discovered", 1)
			}
		}
	}
	if fetchErr != nil {
		w.Metrics.Add("watch_checks_failed", 1)
		return fetchErr
	}
	return w.completeWatchFetch(ctx, t, false)
}
func (w *Worker) terminalWatch(ctx context.Context, t p.Task) error {
	if t.Type == "WATCH_FETCH" {
		return w.completeWatchFetch(ctx, t, true)
	}
	return w.Store.FinishWatch(ctx, t, "FAILED")
}
func (w *Worker) radarTick(ctx context.Context) {
	n, err := w.Store.ScheduleWatches(ctx, time.Now().UTC(), 100)
	if err != nil {
		slog.Warn("watch scheduling failed")
	} else {
		w.Metrics.Add("watch_checks_scheduled", float64(n))
	}
	// Bounded batches rotate through owners. The cursor is only scan progress;
	// notification truth and dedup remain in MySQL and restart safely.
	rows, err := w.Store.DB.QueryContext(ctx, "SELECT id FROM users WHERE id>? ORDER BY id LIMIT 10", w.radarCursor)
	if err != nil {
		return
	}
	users := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		users = append(users, id)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		return
	}
	if len(users) == 0 {
		w.radarCursor = ""
		return
	}
	for _, id := range users {
		created, dedup, err := w.Store.RefreshNotifications(ctx, id)
		if err != nil {
			slog.Warn("notification refresh failed", "category", "READ_MODEL_UNAVAILABLE")
		} else {
			w.Metrics.Add("notifications_created", float64(created))
			w.Metrics.Add("notifications_deduplicated", float64(dedup))
		}
		w.radarCursor = id
	}
}

func (w *Worker) completeWatchFetch(ctx context.Context, t p.Task, failed bool) error {
	succeeded, err := w.Store.CompleteWatchFetch(ctx, t, failed)
	if err == nil && succeeded {
		w.Metrics.Add("watch_checks_succeeded", 1)
	}
	return err
}

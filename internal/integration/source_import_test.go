package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/source"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func importSpecs(adapters ...string) []p.SourceImportSpec {
	out := []p.SourceImportSpec{}
	for _, site := range source.CampusSites() {
		for _, adapter := range adapters {
			if site.Adapter == adapter {
				out = append(out, p.SourceImportSpec{Adapter: site.Adapter, Company: site.Company, URL: site.URL, Scope: site.Scope})
			}
		}
	}
	return out
}
func importTasks(t *testing.T, ctx context.Context, s *p.Store, batch p.SourceImportBatch) []p.Task {
	t.Helper()
	out := []p.Task{}
	for _, item := range batch.Items {
		v, err := p.One[p.Task](ctx, s.DB, "SELECT body FROM outbox WHERE id=?", p.SourceImportTaskID(item.ID, item.Generation))
		must(t, err)
		out = append(out, v)
	}
	return out
}

type bulkImportAdapter struct {
	failResolve bool
	failFetch   bool
}

func (a *bulkImportAdapter) ResolveCampusImport(ctx context.Context, raw string) (source.CampusPreview, error) {
	if a.failResolve && raw == source.MeituanCampusURL {
		return source.CampusPreview{}, &source.FetchError{Category: "SCHEMA_INVALID"}
	}
	return (source.PublicPlatform{}).ResolveCampusImport(ctx, raw)
}
func (a *bulkImportAdapter) Discover(_ context.Context, s d.Source, w d.WatchTarget) ([]source.PostingRef, error) {
	if w.Keyword != "" || w.Direction != "" {
		return nil, p.ErrValidation
	}
	if s.Adapter != "baidu" {
		return []source.PostingRef{}, nil
	}
	return []source.PostingRef{{ExternalID: "one", Title: "后端开发", Company: s.Name, JobType: "FULL_TIME", URL: "https://talent.baidu.com/jobs/one"}, {ExternalID: "two", Title: "服务端开发", Company: s.Name, JobType: "FULL_TIME", URL: "https://talent.baidu.com/jobs/two"}}, nil
}
func (a *bulkImportAdapter) FetchPosting(_ context.Context, _ d.Source, ref source.PostingRef) (source.Result, error) {
	if a.failFetch && ref.ExternalID == "two" {
		return source.Result{}, &source.FetchError{Category: "BLOCKED"}
	}
	return source.Result{Text: jd, Status: "SUCCESS", HTTPStatus: 200}, nil
}
func bulkWatchTask(t *testing.T, ctx context.Context, s *p.Store, id, typ string, generation uint64) p.Task {
	t.Helper()
	v, err := p.One[p.Task](ctx, s.DB, `SELECT body FROM outbox WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.watch_id'))=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.task_type'))=? AND CAST(JSON_UNQUOTE(JSON_EXTRACT(body,'$.schedule_version')) AS UNSIGNED)=? LIMIT 1`, id, typ, generation)
	must(t, err)
	return v
}
func TestSourceImportAllPresetScopesPreservePausedFilteredWatches(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	normal, err := s.CreateCampusSource(ctx, u, "baidu", "GRADUATE", "百度", d.WatchInput{CheckInterval: 3600, Keyword: "只看Go", Enabled: false, Adaptive: true})
	must(t, err)
	var adapters []string
	for _, site := range source.CampusSites() {
		adapters = append(adapters, site.Adapter)
	}
	specs := importSpecs(adapters...)
	var wg sync.WaitGroup
	results := make(chan p.SourceImportBatch, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); v, e := s.StartSourceImport(ctx, u, specs); results <- v; errs <- e }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		must(t, e)
	}
	var first p.SourceImportBatch
	for v := range results {
		if first.ID == "" {
			first = v
		} else if first.ID != v.ID {
			t.Fatal("concurrent clicks created different batches")
		}
	}
	tasks := importTasks(t, ctx, s, first)
	if len(tasks) != len(source.CampusSites()) {
		t.Fatal(len(tasks))
	}
	for i, task := range tasks {
		var resolved source.CampusPreview
		if first.Items[i].Adapter == "xiaohongshu" {
			resolved = source.CampusPreview{Adapter: "xiaohongshu", ProjectCode: "regular-2027", Name: "小红书校招"}
		} else {
			resolved, err = (source.PublicPlatform{}).ResolveCampusImport(ctx, first.Items[i].URL)
			must(t, err)
		}
		_, err = s.PrepareSourceImport(ctx, task)
		must(t, err)
		must(t, s.QueueSourceImport(ctx, task, resolved.Adapter, resolved.ProjectCode, resolved.Name))
		must(t, s.QueueSourceImport(ctx, task, resolved.Adapter, resolved.ProjectCode, resolved.Name)) // duplicate delivery
	}
	restored := &p.Store{DB: s.DB, Weights: s.Weights}
	latest, err := restored.LatestSourceImport(ctx, u)
	must(t, err)
	if latest.ID != first.ID || latest.State != "RUNNING" || len(latest.Items) != len(tasks) {
		t.Fatal(latest)
	}
	listed, err := s.Watches(ctx, u)
	must(t, err)
	if len(listed) != 1 || listed[0].Keyword != "只看Go" || listed[0].Enabled || listed[0].ScheduleVersion != normal.Watch.ScheduleVersion {
		t.Fatalf("watch preference changed: %+v", listed)
	}
	scheduled, err := s.ScheduleWatches(ctx, time.Now().Add(24*time.Hour), 100)
	must(t, err)
	if scheduled != 0 {
		t.Fatal("one-time imports entered periodic scheduling", scheduled)
	}
	other, err := s.NewUser(ctx, d.ID()+"@bulk-import.invalid", "unused")
	must(t, err)
	if _, err = s.RetrySourceImport(ctx, other, first.ID); !errors.Is(err, p.ErrNotFound) {
		t.Fatal("cross-user retry", err)
	}
	for _, item := range latest.Items {
		if _, err = s.Watch(ctx, u, item.WatchID); !errors.Is(err, p.ErrNotFound) {
			t.Fatal("internal watch exposed", err)
		}
		if err = s.DeleteWatch(ctx, u, item.WatchID); !errors.Is(err, p.ErrNotFound) {
			t.Fatal("internal watch deleted", err)
		}
		task := bulkWatchTask(t, ctx, s, item.WatchID, "WATCH_CHECK", 1)
		must(t, s.QueueWatchPostings(ctx, task, []p.Ingest{}))
	}
	latest, err = s.LatestSourceImport(ctx, u)
	must(t, err)
	if latest.State != "COMPLETED" || latest.Completed != len(tasks) || latest.Imported != 0 {
		t.Fatal(latest)
	}
	if _, err = s.StartSourceImport(ctx, u, specs); !errors.Is(err, p.ErrSourceImportCooldown) {
		t.Fatal("completed batch bypassed pacing", err)
	}
	reused, err := s.CreateCampusSource(ctx, u, "meituan", "graduate", "美团", d.WatchInput{CheckInterval: 3600, Enabled: true})
	must(t, err)
	if reused.Existing || reused.Watch.OneShot {
		t.Fatal("one-shot run blocked normal watch registration", reused)
	}
	// Finish a new round after the cooldown and bound completed batch storage.
	for round := 0; round < 3; round++ {
		_, err = s.DB.ExecContext(ctx, "UPDATE source_import_batches SET created_at=created_at-INTERVAL 31 MINUTE WHERE user_id=?", u)
		must(t, err)
		batch, err := s.StartSourceImport(ctx, u, importSpecs("jd"))
		must(t, err)
		must(t, s.FailSourceImport(ctx, importTasks(t, ctx, s, batch)[0], "SOURCE_IMPORT_CHANGED"))
	}
	var kept int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM source_import_batches WHERE user_id=?", u).Scan(&kept))
	if kept != 3 {
		t.Fatal("unbounded batch retention", kept)
	}
	_ = q
}
func TestSourceImportPartialRetryGenerationAndJobDedup(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	batch, err := s.StartSourceImport(ctx, u, importSpecs("baidu", "jd", "meituan"))
	must(t, err)
	fake := &bulkImportAdapter{failResolve: true, failFetch: true}
	w := worker(s, q)
	w.Source = fake
	tasks := importTasks(t, ctx, s, batch)
	var failedPrepare p.Task
	for _, task := range tasks {
		err = w.Process(ctx, task)
		if err != nil {
			failedPrepare = task
			must(t, s.FailSourceImport(ctx, task, "SOURCE_IMPORT_CHANGED"))
		}
	}
	latest, err := s.LatestSourceImport(ctx, u)
	must(t, err)
	var baidu p.SourceImportItem
	for _, item := range latest.Items {
		if item.WatchID == "" {
			continue
		}
		check := bulkWatchTask(t, ctx, s, item.WatchID, "WATCH_CHECK", 1)
		must(t, w.Process(ctx, check))
		must(t, w.Process(ctx, check))
		if item.Adapter == "baidu" {
			baidu = item
		}
	}
	fetches, err := p.Many[p.Task](ctx, s.DB, `SELECT body FROM outbox WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.watch_id'))=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.task_type'))='WATCH_FETCH'`, baidu.WatchID)
	must(t, err)
	if len(fetches) != 2 {
		t.Fatal("duplicate fanout", len(fetches))
	}
	for _, task := range fetches {
		err = w.Process(ctx, task)
		if err != nil {
			_, err = s.CompleteWatchFetch(ctx, task, true)
			must(t, err)
			must(t, s.SourceImportWatchFailed(ctx, task, "SOURCE_IMPORT_BLOCKED"))
		}
		must(t, w.Process(ctx, task)) // stale completed delivery is harmless
	}
	latest, err = s.LatestSourceImport(ctx, u)
	must(t, err)
	if latest.State != "COMPLETED_WITH_ERRORS" || latest.Completed != 3 || latest.Failed != 2 || latest.Imported != 1 {
		t.Fatalf("bad partial counts %+v", latest)
	}
	fake.failResolve = false
	fake.failFetch = false
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.RetrySourceImport(ctx, u, batch.ID); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		must(t, e)
	}
	retried, err := s.LatestSourceImport(ctx, u)
	must(t, err)
	var nextPrepare p.Task
	for _, item := range retried.Items {
		if item.Adapter == "meituan" {
			nextPrepare = importTasks(t, ctx, s, p.SourceImportBatch{Items: []p.SourceImportItem{item}})[0]
		}
	}
	must(t, w.Process(ctx, failedPrepare))
	must(t, s.FailSourceImport(ctx, failedPrepare, "SOURCE_IMPORT_CHANGED")) // old generation cannot clobber retry
	must(t, w.Process(ctx, nextPrepare))
	retried, err = s.LatestSourceImport(ctx, u)
	must(t, err)
	for _, item := range retried.Items {
		if item.State == "EMPTY" {
			continue
		}
		generation := uint64(1)
		if item.Adapter == "baidu" {
			generation = 2
		}
		task := bulkWatchTask(t, ctx, s, item.WatchID, "WATCH_CHECK", generation)
		must(t, w.Process(ctx, task))
		if item.Adapter == "baidu" {
			next, err := p.Many[p.Task](ctx, s.DB, `SELECT body FROM outbox WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.watch_id'))=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.task_type'))='WATCH_FETCH' AND JSON_EXTRACT(body,'$.schedule_version')=2`, item.WatchID)
			must(t, err)
			if len(next) != 2 {
				t.Fatal("retry enqueued duplicate generation", len(next))
			}
			for _, task := range next {
				must(t, w.Process(ctx, task))
				must(t, w.Process(ctx, task))
			}
		}
	}
	latest, err = s.LatestSourceImport(ctx, u)
	must(t, err)
	if latest.State != "COMPLETED" || latest.Failed != 0 || latest.Imported != 2 {
		t.Fatal(latest)
	}
	page, err := s.SourceJobsForUser(ctx, u, baidu.SourceID, 1)
	must(t, err)
	if page.Total != 2 {
		t.Fatal("retry duplicated stored jobs", page.Total)
	}
	for _, old := range fetches {
		must(t, w.Process(ctx, old))
		must(t, s.SourceImportWatchFailed(ctx, old, "SOURCE_IMPORT_BLOCKED"))
	}
	latest, err = s.LatestSourceImport(ctx, u)
	must(t, err)
	if latest.Failed != 0 || latest.Imported != 2 {
		t.Fatal("old task damaged completed retry", latest)
	}
}
func TestSourceImportHTTPSelectsOnlyServerPresets(t *testing.T) {
	_, s, q, u, _ := setup(t)
	authn := auth.Service{Store: s, Secret: []byte("synthetic-source-bulk-http-secret-32")}
	token, err := authn.Token(u)
	must(t, err)
	handler := (&transport.API{Store: s, Queue: q, Auth: authn}).Handler()
	for _, tc := range []struct {
		method, path, body string
		authenticated      bool
		status             int
	}{{"POST", "/api/sources/import-all", "{}", false, 401}, {"POST", "/api/sources/import-all", `{"url":"http://127.0.0.1"}`, true, 400}, {"GET", "/api/sources/imports/latest", "", true, 200}, {"POST", "/api/sources/import-all", "{}", true, 200}, {"POST", "/api/sources/import-all", "{}", true, 200}, {"GET", "/source_bulk.js", "", false, 200}} {
		r := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
		if tc.authenticated {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		if rec.Code != tc.status {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		if tc.path == "/api/sources/import-all" && rec.Code == 200 {
			var v p.SourceImportBatch
			must(t, json.Unmarshal(rec.Body.Bytes(), &v))
			if len(v.Items) != len(source.CampusSites()) {
				t.Fatal("selected manual-only sources or lost preset", v)
			}
		}
	}
}
func TestSourceImportWorkerPersistsTerminalFailure(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	batch, err := s.StartSourceImport(ctx, u, importSpecs("meituan"))
	must(t, err)
	w := worker(s, q)
	w.Source = &bulkImportAdapter{failResolve: true}
	w.Concurrency = 1
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(run) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("bulk worker did not stop")
		}
	})
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		latest, e := s.LatestSourceImport(ctx, u)
		must(t, e)
		if latest.State == "COMPLETED_WITH_ERRORS" {
			if latest.ID != batch.ID || latest.Items[0].Code != "SOURCE_IMPORT_CHANGED" {
				t.Fatal(latest)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("worker did not persist terminal import failure")
}

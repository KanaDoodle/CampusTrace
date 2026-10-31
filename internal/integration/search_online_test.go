package integration

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	a "github.com/KanaDoodle/CampusTrace/internal/searchapi"
	"github.com/KanaDoodle/CampusTrace/internal/searchservice"
)

// Helpers are real killable processes. The child receives only the explicitly
// allocated fixture DSN or loopback Search endpoint, never .env/provider settings.
func TestSearchOnlineProcessHelper(t *testing.T) {
	role := os.Getenv("CAMPUS_ONLINE_CHILD")
	if role == "" {
		t.Skip("subprocess entry")
	}
	ctx := context.Background()
	if role == "worker" {
		s, e := p.Open(ctx, os.Getenv("CAMPUS_ONLINE_DSN"))
		must(t, e)
		defer s.DB.Close()
		s.Search, e = a.NewClient(os.Getenv("CAMPUS_ONLINE_SEARCH"), searchQueryToken, searchWriteToken)
		must(t, e)
		fmt.Println("ready")
		s.RunSearchSync(ctx)
		return
	}
	if role != "search" {
		t.Fatal("unknown helper role")
	}
	backend, e := searchservice.NewBackend(os.Getenv("CAMPUS_ONLINE_INDEX"), os.Getenv("CAMPUS_ONLINE_ALIAS"))
	must(t, e)
	server, e := searchservice.NewServer(backend, searchQueryToken, searchWriteToken)
	must(t, e)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	must(t, e)
	fmt.Println("http://" + listener.Addr().String())
	must(t, http.Serve(listener, server.Handler()))
}
func onlineProcess(t *testing.T, ctx context.Context, role string, env ...string) (string, func()) {
	t.Helper()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSearchOnlineProcessHelper$", "-test.timeout=100s")
	command.Env = append(os.Environ(), append([]string{"CAMPUS_ONLINE_CHILD=" + role}, env...)...)
	stdout, e := command.StdoutPipe()
	must(t, e)
	path := filepath.Join("..", "..", "bin", "verification", "online-rebuild-20261031")
	must(t, os.MkdirAll(path, 0700))
	log, e := os.OpenFile(filepath.Join(path, "child-"+role+"-"+d.ID()+".log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	must(t, e)
	command.Stderr = log
	must(t, command.Start())
	var once sync.Once
	stop := func() { once.Do(func() { _ = command.Process.Kill(); _ = command.Wait(); _ = log.Close() }) }
	t.Cleanup(stop)
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			ready <- scanner.Text()
		} else {
			ready <- ""
		}
	}()
	select {
	case address := <-ready:
		if address == "" {
			t.Fatal("helper failed before readiness", role)
		}
		return address, stop
	case <-ctx.Done():
		stop()
		t.Fatal("helper readiness deadline", role)
	}
	return "", stop
}
func onlineDelete(t *testing.T, ctx context.Context, s *p.Store, id string) {
	t.Helper()
	must(t, s.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "DELETE FROM outbox WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.entity_id')) IN (SELECT id FROM observations WHERE job_id=?) OR JSON_UNQUOTE(JSON_EXTRACT(body,'$.entity_id'))=?", id, id); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, "DELETE FROM agent_events WHERE job_id=?", id); e != nil {
			return e
		}
		for _, query := range []string{"DELETE FROM job_assessment_schedule WHERE job_id=?", "DELETE FROM observations WHERE job_id=?", "DELETE FROM postings WHERE job_id=?", "DELETE FROM jobs WHERE id=?"} {
			if _, e := tx.ExecContext(ctx, query, id); e != nil {
				return e
			}
		}
		return nil
	}))
	_, e := s.DB.ExecContext(ctx, "UPDATE search_revision_state SET next_reconcile_at=NULL WHERE id='jobs'")
	must(t, e)
}
func onlineSet(t *testing.T, out p.JobSearch, want map[string]bool) {
	t.Helper()
	if out.Mode != "fulltext" || len(out.IDs) != len(want) {
		t.Fatal("incomplete online read", out.Mode, len(out.IDs), len(want))
	}
	for _, id := range out.IDs {
		if !want[id] {
			t.Fatal("unexpected online hit", id)
		}
	}
}
func TestSearchOnlineRebuildSurvivesProcessesAndLostAliasReply(t *testing.T) {
	if os.Getenv("OPENSEARCH_TEST_URL") == "" {
		t.Skip("requires disposable Search harness")
	}
	ctx, s, _, owner, _ := kafkaSetup(t)
	alias := "test-online-" + d.ID()
	indexURL, e := url.Parse(os.Getenv("OPENSEARCH_TEST_URL"))
	must(t, e)
	reverse := httputil.NewSingleHostReverseProxy(indexURL)
	var candidate atomic.Value
	candidate.Store("")
	var candidateBulks atomic.Int64
	var pauseCandidate atomic.Bool
	pauseCandidate.Store(true)
	var dropAlias atomic.Bool
	var lostAlias atomic.Int64
	blocked := make(chan struct{})
	var blockOnce sync.Once
	reverse.ModifyResponse = func(response *http.Response) error {
		if response.Request.URL.Path == "/_aliases" && dropAlias.CompareAndSwap(true, false) && response.StatusCode == 200 {
			lostAlias.Add(1)
			_ = response.Body.Close()
			return errors.New("synthetic lost alias acknowledgement")
		}
		return nil
	}
	reverse.ErrorHandler = func(w http.ResponseWriter, r *http.Request, _ error) { http.Error(w, "synthetic unavailable", 503) }
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_bulk" && candidate.Load().(string) != "" {
			raw, e := io.ReadAll(r.Body)
			if e != nil {
				http.Error(w, "read", 503)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			if bytes.Contains(raw, []byte(candidate.Load().(string))) && candidateBulks.Add(1) >= 2 && pauseCandidate.Load() {
				blockOnce.Do(func() { close(blocked) })
				for pauseCandidate.Load() {
					select {
					case <-r.Context().Done():
						return
					case <-time.After(10 * time.Millisecond):
					}
				}
			}
		}
		reverse.ServeHTTP(w, r)
	}))
	t.Cleanup(proxy.Close)
	var target atomic.Value
	var holdCommit atomic.Bool
	holdCommit.Store(true)
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/rebuild/commit" && lostAlias.Load() > 0 && holdCommit.Load() {
			http.Error(w, "synthetic commit retry pause", 503)
			return
		}
		u, e := url.Parse(target.Load().(string))
		if e != nil {
			http.Error(w, "target", 503)
			return
		}
		httputil.NewSingleHostReverseProxy(u).ServeHTTP(w, r)
	}))
	t.Cleanup(relay.Close)
	address, stopSearch := onlineProcess(t, ctx, "search", "CAMPUS_ONLINE_INDEX="+proxy.URL, "CAMPUS_ONLINE_ALIAS="+alias)
	target.Store(address)
	t.Cleanup(func() {
		req, _ := http.NewRequest("DELETE", os.Getenv("OPENSEARCH_TEST_URL")+"/"+alias+"-*", nil)
		if res, e := http.DefaultClient.Do(req); e == nil {
			res.Body.Close()
		}
	})
	client, e := a.NewClient(relay.URL, searchQueryToken, searchWriteToken)
	must(t, e)
	s.Search = client
	inputs := make(map[string]p.Ingest)
	wanted := make(map[string]bool)
	for i := 0; i < 131; i++ {
		input := p.Ingest{Company: "Synthetic Online", Title: fmt.Sprintf("Backend %03d", i), JobType: "FULL_TIME", ExternalID: d.ID(), Text: "Go onlinemarker", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()}
		obs, e := s.IngestForUser(ctx, owner, input)
		must(t, e)
		inputs[obs.JobID] = input
		wanted[obs.JobID] = true
	}
	query := func() p.JobSearch {
		out, e := s.SearchJobs(ctx, owner, a.Query{Text: "onlinemarker", Limit: 1000})
		must(t, e)
		return out
	}
	for i := 0; i < 30; i++ {
		must(t, s.SyncSearchOnce(ctx))
		if query().Mode == "fulltext" {
			break
		}
	}
	onlineSet(t, query(), wanted)
	old, e := client.Status(ctx)
	must(t, e)
	plan, e := client.Rebuild(ctx)
	must(t, e)
	if plan.Generation != old.Generation || plan.Rebuild == nil {
		t.Fatal("operator replaced live index before backfill")
	}
	candidate.Store(strings.Split(plan.Rebuild.Target, ":")[0])
	_, stopWorker := onlineProcess(t, ctx, "worker", "CAMPUS_ONLINE_DSN="+os.Getenv("MYSQL_TEST_DSN"), "CAMPUS_ONLINE_SEARCH="+relay.URL)
	select {
	case <-blocked:
	case <-ctx.Done():
		t.Fatal("candidate did not pause")
	}
	state, e := s.SearchRebuildState(ctx)
	must(t, e)
	if state.Copied < 64 || state.Cursor == "" {
		t.Fatal("no durable checkpoint", state)
	}
	steadyReads := 0
	for i := 0; i < 12; i++ {
		onlineSet(t, query(), wanted)
		steadyReads++
	}
	// Writes during the blocked rebuild commit normally. The independent live
	// synchronizer must still catch them up while the candidate remains blocked.
	ids := make([]string, 0, len(inputs))
	for id := range inputs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	writeStart := time.Now()
	for i := 0; i < 10; i++ {
		in := inputs[ids[i]]
		in.Text = "Go onlinemarker concurrentmarker"
		in.ObservedAt = in.ObservedAt.Add(time.Second)
		_, e = s.IngestForUser(ctx, owner, in)
		must(t, e)
		inputs[ids[i]] = in
	}
	writeElapsed := time.Since(writeStart)
	kafkaWait(t, ctx, "foreground index stalled behind blocked candidate", func() bool {
		out, e := s.SearchJobs(ctx, owner, a.Query{Text: "concurrentmarker", Limit: 1000})
		must(t, e)
		return out.Mode == "fulltext" && len(out.IDs) == 10
	})
	stopSearch()
	stopWorker()
	paused, e := s.SearchRebuildState(ctx)
	must(t, e)
	if paused.Operation != state.Operation || paused.Cursor != state.Cursor {
		t.Fatal("checkpoint changed while candidate was blocked", paused)
	}
	next := inputs[ids[0]]
	next.Text = "Go onlinemarker aftercrashmarker"
	next.ObservedAt = next.ObservedAt.Add(time.Second)
	_, e = s.IngestForUser(ctx, owner, next)
	must(t, e)
	added, e := s.IngestForUser(ctx, owner, p.Ingest{Company: "Synthetic Online", Title: "New during rebuild", JobType: "FULL_TIME", ExternalID: d.ID(), Text: "Go onlinemarker newduringbuild", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, e)
	wanted[added.JobID] = true
	onlineDelete(t, ctx, s, ids[1])
	delete(wanted, ids[1])
	fallback := query()
	if fallback.Mode != "basic" || len(fallback.IDs) != len(wanted) {
		t.Fatal("process outage did not preserve authoritative fallback", fallback.Mode, len(fallback.IDs))
	}
	address, stopSearch = onlineProcess(t, ctx, "search", "CAMPUS_ONLINE_INDEX="+proxy.URL, "CAMPUS_ONLINE_ALIAS="+alias)
	target.Store(address)
	pauseCandidate.Store(false)
	dropAlias.Store(true)
	_, stopWorker = onlineProcess(t, ctx, "worker", "CAMPUS_ONLINE_DSN="+os.Getenv("MYSQL_TEST_DSN"), "CAMPUS_ONLINE_SEARCH="+relay.URL)
	kafkaWait(t, ctx, "lost alias reply was not exercised", func() bool { return lostAlias.Load() == 1 })
	kafkaWait(t, ctx, "Core did not retain its old readable generation", func() bool {
		var generation string
		must(t, s.DB.QueryRowContext(ctx, "SELECT generation FROM search_sync_state WHERE id='jobs'").Scan(&generation))
		out := query()
		return generation == old.Generation && out.Mode == "fulltext"
	})
	interrupted, e := client.Status(ctx)
	must(t, e)
	if interrupted.Generation != plan.Rebuild.Target || interrupted.Rebuild.Phase != "CUTTING" {
		t.Fatal("alias loss was incorrectly treated as no switch", interrupted)
	}
	for i := 0; i < 12; i++ {
		onlineSet(t, query(), wanted)
		steadyReads++
	}
	holdCommit.Store(false)
	kafkaWait(t, ctx, "online handoff did not complete", func() bool {
		r, e := s.SearchRebuildState(ctx)
		must(t, e)
		now, e := client.Status(ctx)
		must(t, e)
		return r.Phase == "DONE" && now.Rebuild.Phase == "DONE" && now.Generation == plan.Rebuild.Target
	})
	onlineSet(t, query(), wanted)
	outsider, e := s.NewUser(ctx, d.ID()+"@online.invalid", "unused")
	must(t, e)
	other, e := s.SearchJobs(ctx, outsider, a.Query{Text: "onlinemarker", Limit: 1000})
	must(t, e)
	if other.Mode != "fulltext" || len(other.IDs) != 0 {
		t.Fatal("rebuild leaked private jobs", other.Mode, len(other.IDs))
	}
	stopWorker()
	rollback, e := client.Rollback(ctx)
	must(t, e)
	if rollback.Rebuild.Target != old.Generation || rollback.Generation != plan.Rebuild.Target {
		t.Fatal("rollback switched before catching up", rollback)
	}
	next.Text = "Go onlinemarker rollbackmarker"
	next.ObservedAt = next.ObservedAt.Add(time.Second)
	_, e = s.IngestForUser(ctx, owner, next)
	must(t, e)
	finishSearchRebuild(t, ctx, s, client)
	restored, e := client.Status(ctx)
	must(t, e)
	if restored.Generation != old.Generation {
		t.Fatal("rollback did not reactivate retained index", restored)
	}
	onlineSet(t, query(), wanted)
	result, e := s.SearchJobs(ctx, owner, a.Query{Text: "rollbackmarker"})
	must(t, e)
	if result.Mode != "fulltext" || len(result.IDs) != 1 {
		t.Fatal("rollback lost concurrent update")
	}
	evidence := map[string]any{"synthetic": true, "initial_jobs": 131, "final_jobs": len(wanted), "steady_fulltext_reads": steadyReads, "steady_fallbacks": 0, "writes_while_candidate_blocked": 10, "writes_seconds": writeElapsed.Seconds(), "checkpoint_copied": state.Copied, "search_process_sigkill": true, "worker_process_sigkill": true, "resumed_same_operation": true, "lost_alias_reply": lostAlias.Load(), "old_generation_readable_after_alias_swap": true, "new_and_updated_jobs_preserved": true, "reconciled_tombstone_preserved": true, "private_acl_preserved": true, "rollback_caught_up_before_switch": true, "limits": "single-host synthetic acceptance; write duration is not a throughput benchmark; writes/outages may legitimately use authoritative fallback"}
	raw, e := json.Marshal(evidence)
	must(t, e)
	t.Log(string(raw))
	if path := os.Getenv("CAMPUS_ONLINE_EVIDENCE"); path != "" {
		must(t, os.WriteFile(path, append(raw, '\n'), 0600))
	}
}

func TestSearchOnlineAbortAndCandidateUUIDFence(t *testing.T) {
	if os.Getenv("OPENSEARCH_TEST_URL") == "" {
		t.Skip("requires disposable Search harness")
	}
	ctx, s, _, owner, _ := kafkaSetup(t)
	client := searchFixture(t, s)
	_, e := s.IngestForUser(ctx, owner, p.Ingest{Company: "Synthetic Abort", Title: "Backend", JobType: "FULL_TIME", ExternalID: d.ID(), Text: "Go abortmarker", FetchStatus: "SUCCESS"})
	must(t, e)
	drainSearch(t, ctx, s, owner, "abortmarker")
	old, e := client.Status(ctx)
	must(t, e)
	// Two independent HTTP sessions contend for one durable operation.
	var wg sync.WaitGroup
	results := make(chan a.Status, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); now, e := client.Rebuild(ctx); results <- now; errs <- e }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil && !errors.Is(e, a.ErrConflict) {
			t.Fatal(e)
		}
	}
	plan, e := client.Rebuild(ctx)
	must(t, e)
	for status := range results {
		if status.Rebuild != nil && status.Rebuild.Operation != plan.Rebuild.Operation {
			t.Fatal("concurrent rebuild created conflicting operations")
		}
	}
	must(t, s.SyncSearchOnce(ctx)) // Claim and durable BACKFILL state, before copying.
	index := strings.Split(plan.Rebuild.Target, ":")[0]
	for _, method := range []string{"DELETE", "PUT"} {
		var body io.Reader
		if method == "PUT" {
			body = strings.NewReader(`{"settings":{"number_of_shards":1,"number_of_replicas":0}}`)
		}
		req, _ := http.NewRequest(method, os.Getenv("OPENSEARCH_TEST_URL")+"/"+index, body)
		req.Header.Set("Content-Type", "application/json")
		res, e := http.DefaultClient.Do(req)
		must(t, e)
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatal("candidate recreation", res.StatusCode)
		}
	}
	if e = s.SyncSearchRebuildOnce(ctx); !errors.Is(e, a.ErrConflict) {
		t.Fatal("candidate UUID change accepted", e)
	}
	out, e := s.SearchJobs(ctx, owner, a.Query{Text: "abortmarker"})
	must(t, e)
	if out.Mode != "fulltext" || len(out.IDs) != 1 {
		t.Fatal("candidate failure disturbed active read")
	}
	status, e := client.Status(ctx)
	must(t, e)
	if status.Generation != old.Generation {
		t.Fatal("candidate failure activated incomplete index")
	}
	_, e = client.AbortRebuild(ctx, a.RebuildRef{Operation: plan.Rebuild.Operation})
	must(t, e)
	must(t, s.SyncSearchOnce(ctx))
	state, e := s.SearchRebuildState(ctx)
	must(t, e)
	if state.Phase != "ABORTED" {
		t.Fatal(state)
	}
	var deltas int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_rebuild_deltas").Scan(&deltas))
	if deltas != 0 {
		t.Fatal("aborted task retained deltas")
	}
	_, e = client.Rebuild(ctx)
	must(t, e)
	finishSearchRebuild(t, ctx, s, client)
	t.Log("CAS reservation, candidate UUID fencing, safe abort and fresh retry passed")
}

func TestSearchOnlineVerificationRejectsSameCountWrongHash(t *testing.T) {
	if os.Getenv("OPENSEARCH_TEST_URL") == "" {
		t.Skip("requires disposable Search harness")
	}
	ctx, s, _, owner, _ := kafkaSetup(t)
	client := searchFixture(t, s)
	obs, e := s.IngestForUser(ctx, owner, p.Ingest{Company: "Synthetic Verify", Title: "Backend", JobType: "FULL_TIME", ExternalID: d.ID(), Text: "Go verifymarker", FetchStatus: "SUCCESS"})
	must(t, e)
	drainSearch(t, ctx, s, owner, "verifymarker")
	old, e := client.Status(ctx)
	must(t, e)
	plan, e := client.Rebuild(ctx)
	must(t, e)
	for i := 0; i < 12; i++ {
		must(t, s.SyncSearchOnce(ctx))
		state, e := s.SearchRebuildState(ctx)
		must(t, e)
		if state.Phase == "VERIFY" {
			break
		}
	}
	state, e := s.SearchRebuildState(ctx)
	must(t, e)
	if state.Phase != "VERIFY" {
		t.Fatal("did not reach visibility verification", state)
	}
	doc, e := p.One[a.Document](ctx, s.DB, "SELECT body FROM search_documents WHERE job_id=?", obs.JobID)
	must(t, e)
	doc.Hash = strings.Repeat("0", 64)
	index := strings.Split(plan.Rebuild.Target, ":")[0]
	raw, e := json.Marshal(doc)
	must(t, e)
	req, _ := http.NewRequest("PUT", os.Getenv("OPENSEARCH_TEST_URL")+"/"+index+"/_doc/"+doc.Dataset+"-"+doc.ID+fmt.Sprintf("?version_type=external&version=%d&refresh=true", doc.Version+100), bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	response, e := http.DefaultClient.Do(req)
	must(t, e)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("fault injection", response.StatusCode)
	}
	if e = s.SyncSearchRebuildOnce(ctx); !errors.Is(e, a.ErrConflict) {
		t.Fatal("same-count candidate corruption was not rejected", e)
	}
	status, e := client.Status(ctx)
	must(t, e)
	if status.Generation != old.Generation {
		t.Fatal("corrupt candidate activated")
	}
	out, e := s.SearchJobs(ctx, owner, a.Query{Text: "verifymarker"})
	must(t, e)
	if out.Mode != "fulltext" || len(out.IDs) != 1 {
		t.Fatal("verification failure disturbed current index")
	}
	_, e = client.AbortRebuild(ctx, a.RebuildRef{Operation: plan.Rebuild.Operation})
	must(t, e)
	must(t, s.SyncSearchOnce(ctx))
	t.Log("equal document counts with a wrong candidate hash did not pass verification")
}

type onlineQueryHandoff struct {
	*a.Client
	after func()
}

func (q *onlineQueryHandoff) Query(ctx context.Context, in a.Query) (a.Result, error) {
	out, e := q.Client.Query(ctx, in)
	if e == nil && q.after != nil {
		q.after()
	}
	return out, e
}
func TestSearchOnlineInFlightReadAcrossCutover(t *testing.T) {
	if os.Getenv("OPENSEARCH_TEST_URL") == "" {
		t.Skip("requires disposable Search harness")
	}
	ctx, s, _, owner, _ := kafkaSetup(t)
	client := searchFixture(t, s)
	input := p.Ingest{Company: "Synthetic Handoff", Title: "Backend", JobType: "FULL_TIME", ExternalID: d.ID(), Text: "Go handoffmarker", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()}
	obs, e := s.IngestForUser(ctx, owner, input)
	must(t, e)
	drainSearch(t, ctx, s, owner, "handoffmarker")
	_, e = client.Rebuild(ctx)
	must(t, e)
	for i := 0; i < 16; i++ {
		must(t, s.SyncSearchOnce(ctx))
		state, e := s.SearchRebuildState(ctx)
		must(t, e)
		if state.Phase == "CATCHUP" {
			break
		}
	}
	state, e := s.SearchRebuildState(ctx)
	must(t, e)
	if state.Phase != "CATCHUP" {
		t.Fatal("did not reach cutover barrier", state)
	}
	s.Search = &onlineQueryHandoff{Client: client, after: func() { must(t, s.SyncSearchRebuildOnce(ctx)) }}
	out, e := s.SearchJobs(ctx, owner, a.Query{Text: "handoffmarker"})
	must(t, e)
	if out.Mode != "fulltext" || len(out.IDs) != 1 || out.IDs[0] != obs.JobID {
		t.Fatal("identical snapshot fell back at handoff", out)
	}
	// A changed authoritative revision is never covered by the handoff exception.
	s.Search = &onlineQueryHandoff{Client: client, after: func() {
		input.Text = "Rust changedmarker"
		input.ObservedAt = input.ObservedAt.Add(time.Second)
		_, e := s.IngestForUser(ctx, owner, input)
		must(t, e)
	}}
	out, e = s.SearchJobs(ctx, owner, a.Query{Text: "handoffmarker"})
	must(t, e)
	if out.Mode != "basic" || len(out.IDs) != 0 {
		t.Fatal("handoff exception admitted stale business data", out)
	}
	t.Log("in-flight read retained fulltext across cutover; a real concurrent write still invalidated it")
}

func TestSearchOnlineLostLeaseCannotRegressPhase(t *testing.T) {
	if os.Getenv("OPENSEARCH_TEST_URL") == "" {
		t.Skip("requires disposable Search harness")
	}
	ctx, s, _, owner, _ := kafkaSetup(t)
	client := searchFixture(t, s)
	_, e := s.IngestForUser(ctx, owner, p.Ingest{Company: "Synthetic Lease", Title: "Backend", JobType: "FULL_TIME", ExternalID: d.ID(), Text: "Go leasemarker", FetchStatus: "SUCCESS"})
	must(t, e)
	drainSearch(t, ctx, s, owner, "leasemarker")
	_, e = client.Rebuild(ctx)
	must(t, e)
	must(t, s.SyncSearchOnce(ctx))
	must(t, s.SyncSearchOnce(ctx)) // Copy is checkpointed; next turn advances to VERIFY.
	state, e := s.SearchRebuildState(ctx)
	must(t, e)
	if state.Phase != "BACKFILL" || state.Copied != 1 {
		t.Fatal("missing copy checkpoint", state)
	}
	blocker, e := s.DB.BeginTx(ctx, nil)
	must(t, e)
	defer blocker.Rollback()
	var operation string
	must(t, blocker.QueryRowContext(ctx, "SELECT operation FROM search_rebuild_state WHERE id='jobs' FOR UPDATE").Scan(&operation))
	done := make(chan error, 1)
	go func() { done <- s.SyncSearchRebuildOnce(ctx) }()
	admin, e := sql.Open("mysql", os.Getenv("MYSQL_TEST_ADMIN_DSN"))
	must(t, e)
	defer admin.Close()
	var schema string
	must(t, s.DB.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&schema))
	kafkaWait(t, ctx, "old owner did not block on phase transition", func() bool {
		var waiting int
		must(t, admin.QueryRowContext(ctx, `SELECT COUNT(*) FROM performance_schema.data_lock_waits w JOIN performance_schema.data_locks l ON w.BLOCKING_ENGINE_LOCK_ID=l.ENGINE_LOCK_ID WHERE l.OBJECT_SCHEMA=? AND l.OBJECT_NAME='search_rebuild_state'`, schema).Scan(&waiting))
		return waiting > 0
	})
	var connection int64
	must(t, s.DB.QueryRowContext(ctx, "SELECT IS_USED_LOCK(CONCAT('ct-search-build:',LEFT(SHA2(DATABASE(),256),32)))").Scan(&connection))
	_, e = admin.ExecContext(ctx, fmt.Sprintf("KILL CONNECTION %d", connection))
	must(t, e)
	// Represent newer owner progress while the old owner's UPDATE is queued.
	_, e = blocker.ExecContext(ctx, "UPDATE search_rebuild_state SET phase='CATCHUP',bootstrap_cursor='' WHERE id='jobs'")
	must(t, e)
	must(t, blocker.Commit())
	select {
	case e = <-done:
		if e != nil && !errors.Is(e, a.ErrConflict) {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("old owner remained blocked")
	}
	current, e := s.SearchRebuildState(ctx)
	must(t, e)
	if current.Phase != "CATCHUP" {
		t.Fatal("old lease regressed phase", current)
	}
	_, e = client.AbortRebuild(ctx, a.RebuildRef{Operation: operation})
	must(t, e)
	must(t, s.SyncSearchOnce(ctx))
	t.Log("queued stale phase transition did not overwrite newer progress after connection-lock loss")
}

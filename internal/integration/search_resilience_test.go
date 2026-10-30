package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	a "github.com/KanaDoodle/CampusTrace/internal/searchapi"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
)

func searchHTTP(t *testing.T, s *p.Store, user string) *httptest.Server {
	t.Helper()
	if s.Metrics == nil {
		s.Metrics = observability.New()
	}
	authn := auth.Service{Store: s, Secret: []byte("synthetic-search-resilience-0123456789")}
	api := &transport.API{Store: s, Auth: authn, Metrics: s.Metrics}
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)
	return server
}
func searchToken(t *testing.T, s *p.Store, user string) string {
	t.Helper()
	token, e := (auth.Service{Store: s, Secret: []byte("synthetic-search-resilience-0123456789")}).Token(user)
	must(t, e)
	return token
}
func queryHTTP(t *testing.T, server *httptest.Server, token, text string) (int, []byte) {
	t.Helper()
	r, e := http.NewRequest("POST", server.URL+"/api/jobs/search", strings.NewReader(d.JSON(map[string]any{"text": text, "limit": 10})))
	must(t, e)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	response, e := server.Client().Do(r)
	must(t, e)
	defer response.Body.Close()
	raw, e := io.ReadAll(response.Body)
	must(t, e)
	return response.StatusCode, raw
}
func TestSearchReadCircuitPreservesACLAndProjectionRecovery(t *testing.T) {
	if os.Getenv("OPENSEARCH_TEST_URL") == "" {
		t.Skip("requires disposable OpenSearch")
	}
	ctx, s, _, owner, source := kafkaSetup(t)
	s.Metrics = observability.New()
	base := searchFixture(t, s)
	input := ingest(t, ctx, s, source)
	input.Text = "Redis resilienceprivate"
	input.SourceID = ""
	private, e := s.IngestForUser(ctx, owner, input)
	must(t, e)
	bob, e := s.NewUser(ctx, d.ID()+"@resilience.invalid", "unused")
	must(t, e)
	input.ExternalID = d.ID()
	input.Company = "Other private company"
	_, e = s.IngestForUser(ctx, bob, input)
	must(t, e)
	drainSearch(t, ctx, s, owner, "resilienceprivate")
	target, e := url.Parse(base.URL)
	must(t, e)
	proxy := httputil.NewSingleHostReverseProxy(target)
	var slow atomic.Bool
	slow.Store(true)
	var attempts atomic.Int32
	fault := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/query" {
			attempts.Add(1)
			if slow.Load() {
				io.Copy(io.Discard, r.Body)
				<-r.Context().Done()
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(fault.Close)
	client, e := a.NewClient(fault.URL, searchQueryToken, searchWriteToken)
	must(t, e)
	policy := a.DefaultQueryPolicy()
	policy.Timeout = 40 * time.Millisecond
	policy.FailureThreshold = 2
	must(t, client.ConfigureQueries(policy, s.Metrics))
	s.Search = client
	for i := 0; i < 2; i++ {
		out, e := s.SearchJobs(ctx, owner, a.Query{Text: "resilienceprivate"})
		must(t, e)
		if out.Mode != "basic" || len(out.IDs) != 1 || out.IDs[0] != private.JobID {
			t.Fatal("fallback lost ACL", out)
		}
	}
	if client.QueryState().State != "open" {
		t.Fatal(client.QueryState())
	}
	before := attempts.Load()
	for i := 0; i < 10; i++ {
		out, e := s.SearchJobs(ctx, bob, a.Query{Text: "resilienceprivate"})
		must(t, e)
		if out.Mode != "basic" || len(out.IDs) != 1 || out.IDs[0] == private.JobID {
			t.Fatal("open-circuit fallback leaked owner job", out)
		}
	}
	if attempts.Load() != before {
		t.Fatal("open circuit still called slow dependency")
	}
	// Query failure must not stop a new write from being indexed via the separate
	// status/apply path. Dirty state also continues to force authoritative fallback.
	input.Company = "Recovered write"
	input.ExternalID = d.ID()
	input.Text = "Redis aftercircuitwrite"
	current, e := s.IngestForUser(ctx, owner, input)
	must(t, e)
	must(t, s.SyncSearchOnce(ctx))
	pending, ready, e := s.SearchSyncStats(ctx)
	must(t, e)
	if pending != 0 || !ready {
		t.Fatal("query circuit blocked indexing", pending, ready)
	}
	slow.Store(false)
	kafkaWait(t, ctx, "query circuit did not recover", func() bool {
		out, e := s.SearchJobs(ctx, owner, a.Query{Text: "aftercircuitwrite"})
		must(t, e)
		return out.Mode == "fulltext" && len(out.IDs) == 1 && out.IDs[0] == current.JobID
	})
	if client.QueryState().State != "closed" {
		t.Fatal(client.QueryState())
	}
	var records map[string]float64
	recorder := httptest.NewRecorder()
	s.Metrics.ServeHTTP(recorder, httptest.NewRequest("GET", "/", nil))
	must(t, json.Unmarshal(recorder.Body.Bytes(), &records))
	if records["search_query_circuit_trips_total"] != 1 || records["search_query_recoveries_total"] != 1 || records["search_fallback_circuit_total"] < 10 {
		t.Fatal("missing operational evidence", records)
	}
}
func TestSearchFallbackStormKeepsWritesAndReturnsExplicitOverload(t *testing.T) {
	ctx, s, _, owner, _ := kafkaSetup(t)
	s.Metrics = observability.New()
	original, e := s.IngestForUser(ctx, owner, p.Ingest{Company: "Synthetic storm", Title: "Go", JobType: "FULL_TIME", FetchStatus: "SUCCESS", Text: "Redis", ObservedAt: time.Now().UTC()})
	must(t, e)
	job, e := s.Job(ctx, original.JobID)
	must(t, e)
	// Synthetic catalog rows do not enqueue analysis. This test isolates read
	// admission, SQL use and a simultaneous real transactional ingestion.
	for offset := 0; offset < 2000; offset += 100 {
		placeholders := []string{}
		values := []any{}
		for i := 0; i < 100; i++ {
			row := job
			row.ID = d.ID()
			row.Fingerprint = d.Hash(row.ID)
			placeholders = append(placeholders, "(?,?,?,?,?,?)")
			values = append(values, row.ID, row.CompanyID, row.Fingerprint, "PRIVATE", owner, d.JSON(row))
		}
		_, e = s.DB.ExecContext(ctx, "INSERT INTO jobs(id,company_id,fingerprint,visibility,owner_id,body) VALUES"+strings.Join(placeholders, ","), values...)
		must(t, e)
	}
	server := searchHTTP(t, s, owner)
	token := searchToken(t, s, owner)
	start := make(chan struct{})
	var group sync.WaitGroup
	var ok, busy, bad, peakFallback atomic.Int32
	for i := 0; i < 100; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			code, raw := queryHTTP(t, server, token, "nomatch-storm-marker")
			switch code {
			case 200:
				var result p.JobSearch
				if json.Unmarshal(raw, &result) != nil || result.Mode != "basic" || len(result.IDs) != 0 {
					bad.Add(1)
				}
				ok.Add(1)
			case 503:
				if !strings.Contains(string(raw), "SEARCH_BUSY") {
					bad.Add(1)
				}
				busy.Add(1)
			default:
				bad.Add(1)
			}
		}()
	}
	sampleStop := make(chan struct{})
	sampleDone := make(chan struct{})
	go func() {
		defer close(sampleDone)
		for {
			select {
			case <-sampleStop:
				return
			default:
				_, _, n, _ := s.SearchReadStats()
				if int32(n) > peakFallback.Load() {
					peakFallback.Store(int32(n))
				}
				time.Sleep(time.Millisecond)
			}
		}
	}()
	close(start)
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	written, e := s.IngestForUser(writeCtx, owner, p.Ingest{Company: "Concurrent ingestion", Title: "Go", JobType: "FULL_TIME", FetchStatus: "SUCCESS", Text: "Redis written-during-read-storm", ObservedAt: time.Now().UTC()})
	must(t, e)
	group.Wait()
	close(sampleStop)
	<-sampleDone
	if written.JobID == "" || ok.Load() == 0 || busy.Load() == 0 || bad.Load() != 0 || ok.Load()+busy.Load() != 100 || peakFallback.Load() > 2 {
		t.Fatal("read storm isolation", ok.Load(), busy.Load(), bad.Load(), peakFallback.Load())
	}
	active, _, fallback, _ := s.SearchReadStats()
	if active != 0 || fallback != 0 {
		t.Fatal("search permits leaked", active, fallback)
	}
	t.Logf("synthetic burst: %d successful bounded reads, %d explicit overloads, peak fallback %d, real write committed", ok.Load(), busy.Load(), peakFallback.Load())
}
func TestSearchFallbackDeadlineCancelsBlockedSQL(t *testing.T) {
	ctx, s, _, owner, _ := kafkaSetup(t)
	_, e := s.IngestForUser(ctx, owner, p.Ingest{Company: "SQL timeout", Title: "Go", JobType: "FULL_TIME", FetchStatus: "SUCCESS", Text: "Redis", ObservedAt: time.Now().UTC()})
	must(t, e)
	blocker, e := s.DB.Conn(ctx)
	must(t, e)
	defer blocker.Close()
	_, e = blocker.ExecContext(ctx, "LOCK TABLES jobs WRITE")
	must(t, e)
	defer blocker.ExecContext(context.Background(), "UNLOCK TABLES")
	_, e = s.SearchJobs(ctx, owner, a.Query{Text: "Redis"})
	if !errors.Is(e, p.ErrSearchBusy) {
		t.Fatal("blocked fallback did not return explicit overload", e)
	}
	active, _, fallback, _ := s.SearchReadStats()
	if active != 0 || fallback != 0 {
		t.Fatal("cancelled SQL leaked permits")
	}
	_, e = blocker.ExecContext(ctx, "UNLOCK TABLES")
	must(t, e)
	result, e := s.SearchJobs(ctx, owner, a.Query{Text: "Redis"})
	must(t, e)
	if result.Mode != "basic" || len(result.IDs) != 1 {
		t.Fatal("did not recover after SQL lock released", result)
	}
}

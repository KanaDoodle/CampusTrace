package integration

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	a "github.com/KanaDoodle/CampusTrace/internal/searchapi"
)

// A recovery response contains only health evidence. An ACL mutation while the
// probe is in flight must still be checked by Core on the next real query.
func TestSearchReadDetachedRecoveryKeepsCurrentACL(t *testing.T) {
	if os.Getenv("OPENSEARCH_TEST_URL") == "" {
		t.Skip("requires disposable OpenSearch")
	}
	ctx, s, _, owner, source := kafkaSetup(t)
	base := searchFixture(t, s)
	input := ingest(t, ctx, s, source)
	input.SourceID, input.Text = "", "Redis detachedrecoveryprivate"
	private, err := s.IngestForUser(ctx, owner, input)
	must(t, err)
	bob, err := s.NewUser(ctx, d.ID()+"@probe.invalid", "unused")
	must(t, err)
	drainSearch(t, ctx, s, owner, "detachedrecoveryprivate")
	target, err := url.Parse(base.URL)
	must(t, err)
	proxy := httputil.NewSingleHostReverseProxy(target)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	fault := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/query" {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			switch calls.Add(1) {
			case 1:
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			case 2:
				close(entered)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(fault.Close)
	client, err := a.NewClient(fault.URL, searchQueryToken, searchWriteToken)
	must(t, err)
	t.Cleanup(client.Close)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	policy := a.DefaultQueryPolicy()
	policy.FailureThreshold, policy.OpenFor = 1, 50*time.Millisecond
	policy.Timeout = time.Second // Extra room for deliberately blocked ACL mutation.
	must(t, client.ConfigureQueries(policy, s.Metrics))
	s.Search = client
	query := a.Query{Text: "detachedrecoveryprivate"}
	out, err := s.SearchJobs(ctx, owner, query)
	must(t, err)
	if out.Mode != "basic" || len(out.IDs) != 1 || out.IDs[0] != private.JobID {
		t.Fatal("initial outage lost authoritative result", out)
	}
	time.Sleep(policy.OpenFor)
	request, cancel := context.WithCancel(ctx)
	defer cancel()
	out, err = s.SearchJobs(request, owner, query)
	must(t, err)
	cancel() // Returning an HTTP response must leave the owned probe alive.
	if out.Mode != "basic" || len(out.IDs) != 1 || out.IDs[0] != private.JobID {
		t.Fatal("probe owner did not receive authoritative fallback", out)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("probe never entered Search")
	}
	if state := client.QueryState(); state.State != "half_open" || state.Active != 1 {
		t.Fatal("foreground waited for or cancelled recovery", state)
	}
	// Deliberately bypass the journal to model a stale ACL in the derived index.
	_, err = s.DB.ExecContext(ctx, "UPDATE jobs SET owner_id=? WHERE id=?", bob, private.JobID)
	must(t, err)
	out, err = s.SearchJobs(ctx, bob, query)
	must(t, err)
	if out.Mode != "basic" || len(out.IDs) != 1 || out.IDs[0] != private.JobID || calls.Load() != 2 {
		t.Fatal("half-open caller bypassed current ACL or started extra probe", out, calls.Load())
	}
	unblock()
	kafkaWait(t, ctx, "detached probe did not recover", func() bool { return client.QueryState().State == "closed" })
	out, err = s.SearchJobs(ctx, owner, query)
	must(t, err)
	if out.Mode != "basic" || len(out.IDs) != 0 || calls.Load() != 3 {
		t.Fatal("healthy probe authorized a stale hit", out, calls.Load())
	}
	active, _, fallback, _ := s.SearchReadStats()
	if active != 0 || fallback != 0 || client.QueryState().Active != 0 {
		t.Fatal("recovery leaked execution slots", active, fallback, client.QueryState())
	}
}

package searchapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/observability"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"
)

// Hold the actual query response behind a barrier: recovery must let the
// foreground caller fall back before the remote query has completed.
func TestQueryRecoveryDoesNotWaitForBlockedProbe(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		close(entered)
		select {
		case <-release:
			json.NewEncoder(w).Encode(Result{Protocol: Protocol, Generation: "generation"})
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)
	client, err := NewClient(server.URL, strings.Repeat("q", 32), "")
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultQueryPolicy()
	policy.FailureThreshold = 1
	if err = client.ConfigureQueries(policy, nil); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	client.guard.now = func() time.Time { return time.Unix(0, now.Load()) }
	q := Query{Generation: "generation", Limit: 10}
	if _, err = client.Query(context.Background(), q); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	now.Add(int64(policy.OpenFor))
	done := make(chan error, 1)
	go func() { _, err := client.Query(context.Background(), q); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("recovery query did not start")
	}
	select {
	case err = <-done:
		if !errors.Is(err, ErrCircuitOpen) {
			t.Fatal("recovery caller must use authoritative fallback", err)
		}
	case <-time.After(75 * time.Millisecond):
		t.Fatal("recovery caller is still waiting for the remote probe")
	}
}

func recoveryClient(t *testing.T, handler http.HandlerFunc) (*Client, *atomic.Int64) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, strings.Repeat("q", 32), "")
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultQueryPolicy()
	policy.FailureThreshold = 1
	if err = client.ConfigureQueries(policy, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	now := &atomic.Int64{}
	now.Store(time.Now().UnixNano())
	client.guard.now = func() time.Time { return time.Unix(0, now.Load()) }
	return client, now
}

func awaitRecoveryState(t *testing.T, client *Client, state string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s := client.QueryState(); s.State == state && s.Active == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("recovery did not settle", client.QueryState())
}

func TestQueryRecoverySurvivesCallerCancellationAndDiscardsResults(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	client, now := recoveryClient(t, func(w http.ResponseWriter, r *http.Request) {
		var q Query
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
			return
		}
		n := calls.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if n == 2 {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		json.NewEncoder(w).Encode(Result{Protocol: Protocol, Generation: q.Generation, Hits: []Hit{{ID: q.User}}})
	})
	q := Query{User: "owner-a", Generation: "generation", Limit: 10}
	if _, err := client.Query(context.Background(), q); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	now.Add(int64(client.guard.policy.OpenFor))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, err := client.Query(ctx, q)
	if !errors.Is(err, ErrCircuitOpen) || len(result.Hits) != 0 {
		t.Fatal("probe response escaped into foreground", result, err)
	}
	cancel() // Typical HTTP handler return must not destroy recovery evidence.
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("probe inherited caller cancellation")
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.Query(context.Background(), q); !errors.Is(err, ErrCircuitOpen) {
				t.Error("extra half-open request", err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 2 || client.QueryState().Active != 1 {
		t.Fatal("probe exceeded single-flight bound", calls.Load(), client.QueryState())
	}
	close(release)
	awaitRecoveryState(t, client, "closed")
	q.User = "owner-b"
	result, err = client.Query(context.Background(), q)
	if err != nil || len(result.Hits) != 1 || result.Hits[0].ID != "owner-b" || calls.Load() != 3 {
		t.Fatal("next user received a probe result instead of a new read", result, err, calls.Load())
	}
}

func TestQueryRecoveryTimeoutBackoffAndShutdown(t *testing.T) {
	var calls atomic.Int32
	client, now := recoveryClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		<-r.Context().Done()
	})
	q := Query{Generation: "generation", Limit: 10}
	if _, err := client.Query(context.Background(), q); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	for _, cooldown := range []time.Duration{4 * time.Second, 8 * time.Second, 8 * time.Second} {
		now.Add(int64(client.QueryState().RetryAfter))
		if _, err := client.Query(context.Background(), q); !errors.Is(err, ErrCircuitOpen) {
			t.Fatal("probe blocked fallback", err)
		}
		awaitRecoveryState(t, client, "open")
		if s := client.QueryState(); s.RetryAfter != cooldown {
			t.Fatal("lost capped recovery backoff", s)
		}
	}
	now.Add(int64(client.QueryState().RetryAfter))
	if _, err := client.Query(context.Background(), q); !errors.Is(err, ErrCircuitOpen) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); client.Close() }()
	}
	wg.Wait()
	before := calls.Load()
	if _, err := client.Query(context.Background(), q); !errors.Is(err, ErrCircuitOpen) || calls.Load() != before || client.QueryState().Active != 0 {
		t.Fatal("shutdown leaked or admitted a probe", err, client.QueryState())
	}
}

func TestQueryRecoveryRejectsCancelledOrInsufficientBudget(t *testing.T) {
	var calls atomic.Int32
	client, now := recoveryClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	q := Query{Generation: "generation", Limit: 10}
	if _, err := client.Query(context.Background(), q); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	now.Add(int64(client.QueryState().RetryAfter))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Query(ctx, q); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := client.Query(ctx, q); !errors.Is(err, ErrQueryBudget) {
		t.Fatal(err)
	}
	if calls.Load() != 1 || client.QueryState().State != "open" || client.QueryState().Active != 0 {
		t.Fatal("unqualified caller triggered recovery work", calls.Load(), client.QueryState())
	}
}

func TestQueryRecoveryRetainsOnlyTraceAndOriginalDeadline(t *testing.T) {
	type privateKey struct{}
	g := newQueryGuard(DefaultQueryPolicy(), observability.New())
	defer g.close()
	g.state = "open"
	state, err := trace.ParseTraceState("private=not-retained")
	if err != nil {
		t.Fatal(err)
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled, TraceState: state})
	parent := trace.ContextWithSpanContext(context.WithValue(context.Background(), privateKey{}, "not-retained"), sc)
	member, err := baggage.NewMember("private", "not-retained")
	if err != nil {
		t.Fatal(err)
	}
	bag, err := baggage.New(member)
	if err != nil {
		t.Fatal(err)
	}
	parent = baggage.ContextWithBaggage(parent, bag)
	deadline := time.Now().Add(300 * time.Millisecond)
	parent, cancel := context.WithDeadline(parent, deadline)
	defer cancel()
	entered, release := make(chan context.Context, 1), make(chan struct{})
	if err := g.runWithProbe(parent, func(context.Context) error { t.Error("ran foreground call"); return nil }, func(ctx context.Context) error {
		entered <- ctx
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}); !errors.Is(err, ErrCircuitOpen) {
		t.Fatal(err)
	}
	var ctx context.Context
	select {
	case ctx = <-entered:
	case <-time.After(time.Second):
		t.Fatal("probe never started")
	}
	cancel()
	gotDeadline, ok := ctx.Deadline()
	gotSpan := trace.SpanContextFromContext(ctx)
	if !ok || !gotDeadline.Equal(deadline.Add(-g.policy.FallbackReserve)) || ctx.Err() != nil {
		t.Fatal("probe lost its original budget or inherited caller cancellation", gotDeadline, ctx.Err())
	}
	if ctx.Value(privateKey{}) != nil || baggage.FromContext(ctx).Len() != 0 || gotSpan.TraceState().Len() != 0 || gotSpan.TraceID() != sc.TraceID() {
		t.Fatal("probe retained private context or lost trace correlation")
	}
	close(release)
	awaitRecoveryState(t, &Client{guard: g}, "closed")
	g.close()
	recorder := httptest.NewRecorder()
	g.metrics.ServeHTTP(recorder, httptest.NewRequest("GET", "/", nil))
	var values map[string]float64
	if err := json.Unmarshal(recorder.Body.Bytes(), &values); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"search_query_attempts_total", "search_query_probes_total", "search_query_probe_fallback_total", "search_query_recoveries_total"} {
		if values[key] != 1 {
			t.Fatal("missing recovery evidence", key, values)
		}
	}
}

func TestQueryRecoveryConcurrentAdmissionAndClose(t *testing.T) {
	for round := 0; round < 100; round++ {
		g := newQueryGuard(DefaultQueryPolicy(), nil)
		g.state = "open"
		var calls atomic.Int32
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				g.runWithProbe(context.Background(), func(context.Context) error { t.Error("ran foreground query"); return nil }, func(context.Context) error {
					calls.Add(1)
					return ErrUnavailable
				})
			}()
		}
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); <-start; g.close() }()
		}
		close(start)
		wg.Wait()
		g.close()
		if g.snapshot().Active != 0 || calls.Load() > 1 {
			t.Fatal("admission/shutdown race leaked a permit or duplicated recovery", round, g.snapshot(), calls.Load())
		}
	}
}

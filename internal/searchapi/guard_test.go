package searchapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func guardPolicy() QueryPolicy {
	p := DefaultQueryPolicy()
	p.Timeout = 20 * time.Millisecond
	p.FallbackReserve = 20 * time.Millisecond
	p.FailureThreshold = 1
	return p
}
func TestQueryCircuitRecoveryAndStaleCompletions(t *testing.T) {
	for _, oldFails := range []bool{false, true} {
		t.Run(map[bool]string{true: "old_failure_cannot_reopen", false: "old_success_cannot_close_probe"}[oldFails], func(t *testing.T) {
			p := guardPolicy()
			p.Timeout = 1400 * time.Millisecond
			g := newQueryGuard(p, nil)
			var nanos atomic.Int64
			nanos.Store(time.Now().UnixNano())
			g.now = func() time.Time { return time.Unix(0, nanos.Load()) }
			started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				done <- g.run(context.Background(), func(context.Context) error {
					close(started)
					<-release
					if oldFails {
						return ErrUnavailable
					}
					return nil
				})
			}()
			<-started
			if e := g.run(context.Background(), func(context.Context) error { return ErrUnavailable }); !errors.Is(e, ErrUnavailable) {
				t.Fatal(e)
			}
			if g.snapshot().State != "open" {
				t.Fatal(g.snapshot())
			}
			called := false
			if e := g.run(context.Background(), func(context.Context) error { called = true; return nil }); !errors.Is(e, ErrCircuitOpen) || called {
				t.Fatal("open circuit performed I/O")
			}
			nanos.Add(int64(g.policy.OpenFor))
			probeStarted, probeRelease, probeDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				probeDone <- g.run(context.Background(), func(context.Context) error { close(probeStarted); <-probeRelease; return nil })
			}()
			<-probeStarted
			for i := 0; i < 50; i++ {
				if e := g.run(context.Background(), func(context.Context) error { t.Error("extra recovery probe"); return nil }); !errors.Is(e, ErrCircuitOpen) {
					t.Fatal(e)
				}
			}
			if oldFails {
				close(probeRelease)
				if e := <-probeDone; e != nil {
					t.Fatal(e)
				}
				close(release)
				<-done
				if state := g.snapshot(); state.State != "closed" || state.ConsecutiveFailures != 0 {
					t.Fatal("old failure reopened healthy circuit", state)
				}
			} else {
				close(release)
				if e := <-done; e != nil {
					t.Fatal(e)
				}
				if g.snapshot().State != "half_open" {
					t.Fatal("old success closed a recovery probe")
				}
				close(probeRelease)
				if e := <-probeDone; e != nil {
					t.Fatal(e)
				}
			}
			if state := g.snapshot(); state.State != "closed" || state.Active != 0 {
				t.Fatal(state)
			}
		})
	}
}
func TestQueryBulkheadHasNoWaitQueue(t *testing.T) {
	p := guardPolicy()
	p.MaxConcurrent = 2
	p.Timeout = 1400 * time.Millisecond
	g := newQueryGuard(p, nil)
	entered, release, done := make(chan struct{}, 2), make(chan struct{}), make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			done <- g.run(context.Background(), func(context.Context) error { entered <- struct{}{}; <-release; return nil })
		}()
	}
	<-entered
	<-entered
	var rejected atomic.Int32
	all := make(chan struct{}, 100)
	for i := 0; i < 100; i++ {
		go func() {
			e := g.run(context.Background(), func(context.Context) error { return errors.New("unexpected remote call") })
			if errors.Is(e, ErrQueryBusy) {
				rejected.Add(1)
			}
			all <- struct{}{}
		}()
	}
	for i := 0; i < 100; i++ {
		<-all
	}
	if rejected.Load() != 100 || g.snapshot().Active != 2 {
		t.Fatal("saturated calls queued or exceeded admission", rejected.Load(), g.snapshot())
	}
	close(release)
	<-done
	<-done
	if g.snapshot().Active != 0 {
		t.Fatal("leaked permits")
	}
}
func TestQueryCancellationAndParentBudgetDoNotPoisonHealth(t *testing.T) {
	g := newQueryGuard(guardPolicy(), nil)
	short, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	called := false
	if e := g.run(short, func(context.Context) error { called = true; return nil }); !errors.Is(e, ErrQueryBudget) || called {
		t.Fatal("fallback budget not reserved", e)
	}
	g.policy.Timeout = 200 * time.Millisecond
	limited, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	if e := g.run(limited, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }); !errors.Is(e, ErrQueryBudget) && !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	parent, cancelParent := context.WithCancel(context.Background())
	if e := g.run(parent, func(ctx context.Context) error { cancelParent(); <-ctx.Done(); return ctx.Err() }); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if state := g.snapshot(); state.State != "closed" || state.ConsecutiveFailures != 0 || state.Active != 0 {
		t.Fatal(state)
	}
}
func TestQueryTimeoutTripsButProtocolConflictsDoNot(t *testing.T) {
	g := newQueryGuard(guardPolicy(), nil)
	for _, e := range []error{ErrConflict, ErrInvalid} {
		if got := g.run(context.Background(), func(context.Context) error { return e }); !errors.Is(got, e) {
			t.Fatal(got)
		}
	}
	if g.snapshot().State != "closed" {
		t.Fatal("semantic conflict tripped circuit")
	}
	if e := g.run(context.Background(), func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	if g.snapshot().State != "open" {
		t.Fatal("dependency timeout did not trip")
	}
	var nanos atomic.Int64
	nanos.Store(time.Now().Add(time.Minute).UnixNano())
	g.now = func() time.Time { return time.Unix(0, nanos.Load()) }
	if e := g.run(context.Background(), func(context.Context) error { return ErrUnavailable }); e == nil {
		t.Fatal("missing probe failure")
	}
	state := g.snapshot()
	if state.State != "open" || state.RetryAfter != 2*g.policy.OpenFor {
		t.Fatal("probe backoff", state)
	}
}
func TestQueryCircuitDoesNotBlockProjectionRecovery(t *testing.T) {
	var queries, statuses, applies atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		switch r.URL.Path {
		case "/v1/query":
			queries.Add(1)
			<-r.Context().Done()
		case "/v1/status":
			statuses.Add(1)
			json.NewEncoder(w).Encode(Status{Protocol: Protocol, Generation: "generation"})
		case "/v1/batch":
			applies.Add(1)
			json.NewEncoder(w).Encode(Status{Protocol: Protocol, Generation: "generation"})
		}
	}))
	defer server.Close()
	client, e := NewClient(server.URL, "query-token-012345678901234567890123", "write-token-012345678901234567890123")
	if e != nil {
		t.Fatal(e)
	}
	if e = client.ConfigureQueries(guardPolicy(), nil); e != nil {
		t.Fatal(e)
	}
	q := Query{Generation: "generation", Limit: 10}
	if _, e = client.Query(context.Background(), q); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	if _, e = client.Query(context.Background(), q); !errors.Is(e, ErrCircuitOpen) {
		t.Fatal(e)
	}
	if _, e = client.Status(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = client.Apply(context.Background(), Batch{Protocol: Protocol, Generation: "generation", Documents: []Document{{}}}); e != nil {
		t.Fatal(e)
	}
	if queries.Load() != 1 || statuses.Load() != 1 || applies.Load() != 1 {
		t.Fatal("query protection affected recovery writes/status")
	}
}
func TestQueryPolicyConfigurationFailsClosed(t *testing.T) {
	t.Setenv("SEARCH_FALLBACK_RESERVE_MS", "150")
	for _, value := range []string{"0", "10", "1500", "1501", "bad", "99999999999999999999"} {
		t.Setenv("SEARCH_QUERY_TIMEOUT_MS", value)
		if _, e := QueryPolicyFromEnv(); e == nil {
			t.Fatal("invalid policy accepted", value)
		}
	}
	t.Setenv("SEARCH_QUERY_TIMEOUT_MS", "250")
	p, e := QueryPolicyFromEnv()
	if e != nil || p.Timeout != 250*time.Millisecond {
		t.Fatal(p, e)
	}
}

func TestQueryReservesAnAbsoluteParentDeadline(t *testing.T) {
	p := DefaultQueryPolicy()
	p.Timeout = 1400 * time.Millisecond
	p.FallbackReserve = 100 * time.Millisecond
	g := newQueryGuard(p, nil)
	deadline := time.Now().Add(500 * time.Millisecond)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	var got time.Time
	if e := g.run(ctx, func(child context.Context) error { got, _ = child.Deadline(); return nil }); e != nil {
		t.Fatal(e)
	}
	want := deadline.Add(-p.FallbackReserve)
	if !got.Equal(want) {
		t.Fatal("fallback reserve drifted during admission", got, want)
	}
}

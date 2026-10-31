package searchapi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/observability"
)

const RequestBudget = 1500 * time.Millisecond

var ErrCircuitOpen = errors.New("search query circuit open")
var ErrQueryBusy = errors.New("search query concurrency exhausted")
var ErrQueryBudget = errors.New("search query budget reserved for fallback")

// QueryPolicy applies only to read queries. Projection writes and index status
// use their existing independent timeout/retry path, so recovery can progress.
type QueryPolicy struct {
	Timeout, FallbackReserve, OpenFor, MaxOpenFor time.Duration
	MaxConcurrent, FailureThreshold               int
}

func DefaultQueryPolicy() QueryPolicy {
	return QueryPolicy{Timeout: 200 * time.Millisecond, FallbackReserve: 150 * time.Millisecond, OpenFor: 2 * time.Second, MaxOpenFor: 8 * time.Second, MaxConcurrent: 8, FailureThreshold: 3}
}
func (p QueryPolicy) Valid() bool {
	return p.Timeout >= 20*time.Millisecond && p.Timeout <= RequestBudget && p.Timeout+p.FallbackReserve <= RequestBudget && p.FallbackReserve >= 20*time.Millisecond && p.FallbackReserve <= time.Second && p.OpenFor >= 50*time.Millisecond && p.MaxOpenFor >= p.OpenFor && p.MaxOpenFor <= time.Minute && p.MaxConcurrent >= 1 && p.MaxConcurrent <= 64 && p.FailureThreshold >= 1 && p.FailureThreshold <= 20
}
func QueryPolicyFromEnv() (QueryPolicy, error) {
	p := DefaultQueryPolicy()
	for key, target := range map[string]*time.Duration{"SEARCH_QUERY_TIMEOUT_MS": &p.Timeout, "SEARCH_FALLBACK_RESERVE_MS": &p.FallbackReserve} {
		if raw := os.Getenv(key); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil || n < 0 || n > 60000 {
				return p, fmt.Errorf("invalid %s", key)
			}
			*target = time.Duration(n) * time.Millisecond
		}
	}
	if !p.Valid() {
		return p, fmt.Errorf("invalid Search query policy")
	}
	return p, nil
}

type QueryState struct {
	State                              string
	Active, Limit, ConsecutiveFailures int
	RetryAfter                         time.Duration
}
type queryTicket struct {
	epoch uint64
	probe bool
}

type queryGuard struct {
	mu               sync.Mutex
	policy           QueryPolicy
	metrics          *observability.Metrics
	now              func() time.Time
	active, failures int
	state            string
	epoch            uint64
	until            time.Time
	cooldown         time.Duration
	stopped          bool
	probeCancel      context.CancelFunc
	probeDone        chan struct{}
}

func newQueryGuard(p QueryPolicy, m *observability.Metrics) *queryGuard {
	return &queryGuard{policy: p, metrics: m, now: time.Now, state: "closed", cooldown: p.OpenFor}
}
func (g *queryGuard) snapshot() QueryState {
	g.mu.Lock()
	defer g.mu.Unlock()
	left := g.until.Sub(g.now())
	if left < 0 {
		left = 0
	}
	return QueryState{g.state, g.active, g.policy.MaxConcurrent, g.failures, left}
}
func (g *queryGuard) add(name string) {
	if g.metrics != nil {
		g.metrics.Add(name, 1)
	}
}
func (g *queryGuard) admit() (queryTicket, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped || g.state == "half_open" || g.state == "open" && g.now().Before(g.until) {
		return queryTicket{}, ErrCircuitOpen
	}
	if g.active >= g.policy.MaxConcurrent {
		return queryTicket{}, ErrQueryBusy
	}
	probe := g.state == "open"
	if probe {
		g.state = "half_open"
		g.add("search_query_probes_total")
	}
	g.active++
	return queryTicket{g.epoch, probe}, nil
}

// outcome: -1 dependency failure; 0 neutral caller cancellation/budget; 1 health
// evidence. Epoch fencing prevents late calls from undoing a newer open circuit.
func (g *queryGuard) finish(t queryTicket, outcome int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active--
	if t.epoch != g.epoch {
		return
	}
	if outcome == 1 {
		g.failures = 0
		if t.probe {
			g.state = "closed"
			g.epoch++
			g.cooldown = g.policy.OpenFor
			g.until = time.Time{}
			g.add("search_query_recoveries_total")
		}
		return
	}
	if outcome == 0 {
		if t.probe {
			g.state = "open"
			g.until = g.now().Add(g.cooldown)
			g.epoch++
		}
		return
	}
	g.failures++
	if t.probe || g.failures >= g.policy.FailureThreshold {
		if t.probe {
			g.cooldown *= 2
			if g.cooldown > g.policy.MaxOpenFor {
				g.cooldown = g.policy.MaxOpenFor
			}
		}
		g.state = "open"
		g.until = g.now().Add(g.cooldown)
		g.epoch++
		g.add("search_query_circuit_trips_total")
	}
}
func (g *queryGuard) run(ctx context.Context, call func(context.Context) error) error {
	return g.runWithProbe(ctx, call, nil)
}

// probe must own its response storage: it may finish after this method returns.
// Only half-open recovery is detached; ordinary queries retain caller cancellation.
func (g *queryGuard) runWithProbe(ctx context.Context, call, probe func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	queryDeadline := time.Now().Add(g.policy.Timeout)
	parentLimited := false
	if deadline, ok := ctx.Deadline(); ok {
		reserved := deadline.Add(-g.policy.FallbackReserve)
		if !reserved.After(time.Now()) {
			g.add("search_query_budget_rejected_total")
			return ErrQueryBudget
		}
		if reserved.Before(queryDeadline) {
			queryDeadline = reserved
			parentLimited = true
		}
	}
	ticket, err := g.admit()
	if err != nil {
		if errors.Is(err, ErrCircuitOpen) {
			g.add("search_query_circuit_rejected_total")
		} else {
			g.add("search_query_busy_rejected_total")
		}
		return err
	}
	// An absolute deadline preserves the reserve even if admission was delayed.
	if !queryDeadline.After(time.Now()) {
		g.finish(ticket, 0)
		g.add("search_query_budget_rejected_total")
		return ErrQueryBudget
	}
	if ticket.probe && probe != nil {
		g.startProbe(ctx, ticket, queryDeadline, parentLimited, probe)
		g.add("search_query_probe_fallback_total")
		g.add("search_query_circuit_rejected_total")
		return ErrCircuitOpen
	}
	return g.perform(ctx, ticket, queryDeadline, parentLimited, call)
}

func (g *queryGuard) perform(ctx context.Context, ticket queryTicket, queryDeadline time.Time, parentLimited bool, call func(context.Context) error) error {
	child, cancel := context.WithDeadline(ctx, queryDeadline)
	started := time.Now()
	g.add("search_query_attempts_total")
	err := call(child)
	childErr, parentErr := child.Err(), ctx.Err()
	cancel()
	outcome := 1
	switch {
	case parentErr != nil:
		outcome = 0
		err = parentErr
		g.add("search_query_cancelled_total")
	case errors.Is(childErr, context.DeadlineExceeded) && parentLimited:
		outcome = 0
		err = ErrQueryBudget
		g.add("search_query_budget_rejected_total")
	case errors.Is(childErr, context.DeadlineExceeded):
		outcome = -1
		err = ErrUnavailable
		g.add("search_query_timeouts_total")
	case err != nil && !errors.Is(err, ErrConflict) && !errors.Is(err, ErrInvalid):
		outcome = -1
	}
	if outcome < 0 {
		g.add("search_query_dependency_errors_total")
	}
	g.finish(ticket, outcome)
	if g.metrics != nil {
		g.metrics.Observe("search_query_remote_seconds", time.Since(started).Seconds())
	}
	return err
}

func (g *queryGuard) startProbe(parent context.Context, ticket queryTicket, deadline time.Time, parentLimited bool, call func(context.Context) error) {
	// Retain only trace correlation, not caller credentials or arbitrary context
	// values. The original absolute remote deadline still bounds detached work.
	ctx, cancel := context.WithCancel(observability.Extract(context.Background(), observability.TraceParent(parent)))
	done := make(chan struct{})
	g.mu.Lock()
	if g.stopped {
		g.mu.Unlock()
		cancel()
		g.finish(ticket, 0)
		return
	}
	g.probeCancel, g.probeDone = cancel, done
	go func() {
		defer close(done)
		defer cancel()
		ctx, span := observability.Start(ctx, "search.query.probe")
		err := g.perform(ctx, ticket, deadline, parentLimited, call)
		observability.End(span, err)
	}()
	g.mu.Unlock()
}

// close prevents new admissions, cancels the one owned recovery probe and joins
// it. Admission and publication of probeDone share the mutex, avoiding Add/Wait
// races and allowing concurrent, repeated shutdown calls.
func (g *queryGuard) close() {
	g.mu.Lock()
	g.stopped = true
	cancel, done := g.probeCancel, g.probeDone
	g.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/pipeline"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type schedulerAnalyzer struct {
	onlyObservation  string
	calls            atomic.Int32
	entered, release chan struct{}
	failure          error
}

func (a *schedulerAnalyzer) Analyze(ctx context.Context, o d.Observation, _ string) ([]d.Claim, error) {
	// Dispatch reads the shared SQL outbox, so a full-suite worker can also see
	// unrelated synthetic catalog work. Barriers/counters belong to this test's
	// selected observation, not whichever unrelated message wins the first slot.
	if a.onlyObservation != "" && o.ID != a.onlyObservation {
		return []d.Claim{}, nil
	}
	if a.calls.Add(1) == 1 && a.entered != nil {
		close(a.entered)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-a.release:
		}
	}
	return []d.Claim{}, a.failure
}
func TestSchedulerWorkerLaneIsolationAndDurableUnknownFence(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	q.Partitioned = true
	q.ResolveOwner = s.TaskOwner
	o, e := s.IngestForUser(ctx, u, p.Ingest{Company: "Synthetic Scheduler", Title: "后端", JobType: "FULL_TIME", ExternalID: d.ID(), Text: "熟悉 Go。", FetchStatus: "SUCCESS"})
	must(t, e)
	model := &schedulerAnalyzer{onlyObservation: o.ID, entered: make(chan struct{}), release: make(chan struct{})}
	w := &pipeline.Worker{Store: s, Queue: q, Analyzer: model, Concurrency: 1, Timeout: 2 * time.Second, ClaimIdle: 3 * time.Second, MaxAttempts: 3, Metrics: observability.New()}
	workCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(workCtx) }()
	t.Cleanup(func() {
		if done != nil {
			cancel()
			must(t, <-done)
		}
	})
	select {
	case <-model.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker analysis did not start")
	}
	task := p.NewTask("ASSESS", o.JobID)
	task.OwnerID = u
	must(t, q.Publish(ctx, task))
	deadline := time.Now().Add(1800 * time.Millisecond)
	completed := false
	for time.Now().Before(deadline) {
		var n int
		must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM completed_tasks WHERE id=?", task.ID).Scan(&n))
		if n == 1 {
			completed = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !completed {
		t.Fatal("ASSESS blocked behind slow ANALYZE")
	}
	close(model.release)
	cancel()
	must(t, <-done)
	done = nil
	// A new observation represents a previously sent external call with no saved
	// response. A fresh worker must not call it again after restart.
	o2, e := s.IngestForUser(ctx, u, p.Ingest{Company: "Synthetic Scheduler", Title: "另一个岗位", JobType: "FULL_TIME", ExternalID: d.ID(), Text: "另一个任务，熟悉 Go。", FetchStatus: "SUCCESS"})
	must(t, e)
	t2, e := s.BindAnalysis(ctx, p.NewTask("ANALYZE", o2.ID), d.ProcessingVersion(d.AnalysisVersion, d.ParserVersion, o2.ParserVersion))
	must(t, e)
	began, e := s.BeginAnalysisInvocation(ctx, t2)
	must(t, e)
	if !began {
		t.Fatal("fence not installed")
	}
	another := &schedulerAnalyzer{}
	restart := &pipeline.Worker{Store: s, Queue: q, Analyzer: another, Metrics: observability.New()}
	if e = restart.Process(ctx, t2); !errors.Is(e, pipeline.ErrAnalysisUncertain) {
		t.Fatal(e)
	}
	if another.calls.Load() != 0 {
		t.Fatal("unknown invocation repeated")
	}
}
func TestSchedulerOutboxFairRoutingBackfillAndSaturatedType(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	_, _, _, other, _ := setup(t)
	for _, owner := range []string{u, u, u, other} {
		o, e := s.IngestForUser(ctx, owner, p.Ingest{Company: "Synthetic Fair", Title: "后端", JobType: "FULL_TIME", ExternalID: d.ID(), Text: "Go 开发", FetchStatus: "SUCCESS"})
		must(t, e)
		must(t, s.Tx(ctx, func(tx *sql.Tx) error { return p.Outbox(ctx, tx, p.NewTask("ASSESS", o.JobID)) }))
	}
	// An old row without tenant metadata must survive upgrade and be routed.
	_, e := s.DB.ExecContext(ctx, "DELETE FROM outbox_tenants WHERE owner_id=?", other)
	must(t, e)
	n, e := s.BackfillOutboxTenants(ctx)
	must(t, e)
	if n < 1 {
		t.Fatal("legacy row not backfilled")
	}
	seen := map[string]map[string]int{}
	totalSent := 0
	for turn := 0; turn < 2; turn++ {
		n, e = s.FairOutbox(ctx, func(_ context.Context, t p.Task) (bool, error) {
			if seen[t.OwnerID] == nil {
				seen[t.OwnerID] = map[string]int{}
			}
			seen[t.OwnerID][t.Type]++
			return t.Type != "ANALYZE", nil
		})
		must(t, e)
		totalSent += n
	}
	if seen[u]["ANALYZE"] < 1 || seen[other]["ANALYZE"] < 1 || seen[u]["ASSESS"] < 1 || seen[other]["ASSESS"] < 1 || totalSent < 2 {
		t.Fatal("one saturated type blocked other types or owners", seen, n)
	}
	// A full resident lane must leave its outbox source unsent and recoverable.
	q.Partitioned = true
	q.Capacity = 1
	q.OwnerCapacity = 1
	q.ResolveOwner = s.TaskOwner
	must(t, q.Init(ctx))
	w := worker(s, q)
	must(t, w.Dispatch(ctx))
	must(t, w.Dispatch(ctx))
	stats, e := q.Stats(ctx)
	must(t, e)
	for _, stat := range stats {
		if stat.Outstanding > 1 {
			t.Fatal(stat)
		}
	}
	var pending int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM outbox WHERE sent=FALSE").Scan(&pending))
	if pending < 2 {
		t.Fatal("full queue lost durable tasks", pending)
	}
}

func TestSchedulerBackgroundRejectionRetriesButUnknownOutcomeDoesNot(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{true: "unknown", false: "429"}[unknown], func(t *testing.T) {
			ctx, s, q, u, _ := setup(t)
			o, e := s.IngestForUser(ctx, u, p.Ingest{Company: "Synthetic Rejection", Title: "后端", JobType: "FULL_TIME", ExternalID: d.ID(), Text: "Go 开发", FetchStatus: "SUCCESS"})
			must(t, e)
			failure := error(&analysis.HTTPError{Status: 429})
			if unknown {
				failure = context.DeadlineExceeded
			}
			a := &schedulerAnalyzer{failure: failure}
			w := worker(s, q)
			w.Analyzer = a
			task := p.NewTask("ANALYZE", o.ID)
			for i := 0; i < 2; i++ {
				e = w.Process(ctx, task)
				if unknown && !errors.Is(e, pipeline.ErrAnalysisUncertain) {
					t.Fatal(e)
				}
				if !unknown && !pipeline.Transient(e) {
					t.Fatal(e)
				}
			}
			expected := int32(2)
			if unknown {
				expected = 1
			}
			if a.calls.Load() != expected {
				t.Fatal("incorrect replay", a.calls.Load(), expected)
			}
		})
	}
}

type schedulerModel struct {
	delegate   *wholeModel
	entered    chan struct{}
	unblock    chan struct{}
	calls      atomic.Int32
	failure    error
	timeout    bool
	mu         sync.Mutex
	batchSizes []int
}

func TestSchedulerWorkerRestartReclaimsPendingWithoutRepeatingUnknownCall(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	q.Partitioned = true
	q.ResolveOwner = s.TaskOwner
	o, e := s.IngestForUser(ctx, u, p.Ingest{Company: "Synthetic Restart", Title: "后端", JobType: "FULL_TIME", ExternalID: d.ID(), Text: "Go 任务恢复", FetchStatus: "SUCCESS"})
	must(t, e)
	initial := &schedulerAnalyzer{onlyObservation: o.ID, entered: make(chan struct{}), release: make(chan struct{})}
	startWorker := func(a *schedulerAnalyzer) (context.CancelFunc, <-chan error) {
		w := worker(s, q)
		w.Analyzer = a
		w.Timeout = 500 * time.Millisecond
		w.ClaimIdle = time.Second
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- w.Run(runCtx) }()
		return cancel, done
	}
	cancel, done := startWorker(initial)
	select {
	case <-initial.entered:
	case <-time.After(5 * time.Second):
		cancel()
		<-done
		t.Fatal("initial invocation did not begin")
	}
	cancel()
	must(t, <-done)
	restarted := &schedulerAnalyzer{onlyObservation: o.ID}
	cancel, done = startWorker(restarted)
	defer func() { cancel(); must(t, <-done) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		v, e := s.Observation(ctx, o.ID)
		must(t, e)
		if v.ErrorCategory == "ANALYSIS_OUTCOME_UNKNOWN" {
			if restarted.calls.Load() != 0 {
				t.Fatal("unknown paid invocation replayed")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("pending unknown analysis did not recover into manual review")
}

func (m *schedulerModel) Complete(ctx context.Context, messages, schema any) (json.RawMessage, error) {
	n := m.calls.Add(1)
	var msgs []map[string]string
	_ = json.Unmarshal([]byte(d.JSON(messages)), &msgs)
	var in matching.HolisticRequest
	_ = json.Unmarshal([]byte(msgs[1]["content"]), &in)
	m.mu.Lock()
	m.batchSizes = append(m.batchSizes, len(in.Jobs))
	m.mu.Unlock()
	if n == 1 && m.entered != nil {
		close(m.entered)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-m.unblock:
		}
	}
	if m.timeout {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if m.failure != nil {
		return nil, m.failure
	}
	return m.delegate.Complete(ctx, messages, schema)
}
func schedulerSubmit(t *testing.T, ctx context.Context, s *p.Store, u, token string, ids []string, h http.Handler) p.MatchRun {
	t.Helper()
	snap, e := s.MatchSnapshot(ctx, u, matching.ModelIdentity("server-default", "whole-fixture"), "", ids)
	must(t, e)
	rec := matchingRequest(h, token, "/api/matching/tasks", "POST", map[string]any{"job_ids": ids, "candidate_hash": snap.CandidateHash, "input_keys": taskInputKeys(snap, ids), "request_key": d.ID()})
	if rec.Code != 202 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var run p.MatchRun
	must(t, json.Unmarshal(rec.Body.Bytes(), &run))
	return run
}
func schedulerQueued(t *testing.T, ctx context.Context, s *p.Store, u, id string) p.MatchRun {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		v, e := s.MatchRun(ctx, u, id)
		must(t, e)
		if v.ExecutionState == "QUEUED" {
			return v
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("run never queued")
	return p.MatchRun{}
}
func TestSchedulerMatchingYieldsBetweenBatches(t *testing.T) {
	ctx, s, u, token, ids, api, h, delegate := wholeSetup(t)
	_, _, small, smallToken, smallIDs, _, _, _ := wholeSetup(t)
	for i := 0; i < 5; i++ {
		o, e := s.IngestForUser(ctx, u, p.Ingest{Company: "Whole Synthetic", Title: "批量岗位", JobType: "FULL_TIME", ExternalID: d.ID(), Text: "任职要求：熟悉Go。工作职责：开发后端服务并处理可靠性问题。", FetchStatus: "SUCCESS"})
		must(t, e)
		ids = append(ids, o.JobID)
	}
	model := &schedulerModel{delegate: delegate, entered: make(chan struct{}), unblock: make(chan struct{})}
	api.ResumeModel = model
	runner := api.StartMatchTasks(ctx, 1)
	t.Cleanup(runner.Close)
	bigRun := schedulerSubmit(t, ctx, s, u, token, ids, h)
	select {
	case <-model.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("model did not start")
	}
	littleRun := schedulerSubmit(t, ctx, s, small, smallToken, smallIDs[:1], h)
	schedulerQueued(t, ctx, s, small, littleRun.ID)
	close(model.unblock)
	awaitRun(t, s, small, littleRun.ID, "COMPLETED")
	awaitRun(t, s, u, bigRun.ID, "COMPLETED")
	model.mu.Lock()
	defer model.mu.Unlock()
	if len(model.batchSizes) != 4 || model.batchSizes[0] != 3 || model.batchSizes[1] != 1 {
		t.Fatal("small request did not run between large quanta", model.batchSizes)
	}
}
func TestSchedulerQueueDeadlineCancelAndPauseDoNotCallProvider(t *testing.T) {
	for _, action := range []string{"DEADLINE", "CANCEL", "PAUSE"} {
		t.Run(action, func(t *testing.T) {
			ctx, s, u, token, ids, api, h, delegate := wholeSetup(t)
			_, _, small, st, si, _, _, _ := wholeSetup(t)
			model := &schedulerModel{delegate: delegate, entered: make(chan struct{}), unblock: make(chan struct{})}
			api.ResumeModel = model
			runner := api.StartMatchTasks(ctx, 1)
			runner.WaitTimeout = 150 * time.Millisecond
			t.Cleanup(runner.Close)
			big := schedulerSubmit(t, ctx, s, u, token, ids, h)
			<-model.entered
			little := schedulerSubmit(t, ctx, s, small, st, si[:1], h)
			schedulerQueued(t, ctx, s, small, little.ID)
			expected := "WAITING_AUTH"
			if action != "DEADLINE" {
				expected = map[string]string{"CANCEL": "CANCELLED", "PAUSE": "PAUSED"}[action]
				deadline := time.Now().Add(time.Second)
				for {
					current, e := s.MatchRun(ctx, small, little.ID)
					must(t, e)
					rec := matchingRequest(h, st, "/api/matching/tasks/"+little.ID+"/control", "POST", map[string]any{"version": current.Version, "action": action})
					if rec.Code == 200 {
						break
					}
					if rec.Code != 409 || time.Now().After(deadline) {
						t.Fatal(rec.Code, rec.Body.String())
					}
				}
			}
			stopped := awaitRun(t, s, small, little.ID, expected)
			if action == "DEADLINE" && (stopped.ExecutionState != "MATCH_QUEUE_TIMEOUT" || stopped.Items[0].State != "QUEUED") {
				t.Fatal(stopped)
			}
			if model.calls.Load() != 1 {
				t.Fatal("waiting invoked model")
			}
			close(model.unblock)
			awaitRun(t, s, u, big.ID, "COMPLETED")
			if model.calls.Load() != 1 {
				t.Fatal("stopped request invoked provider", model.calls.Load())
			}
		})
	}
}
func TestSchedulerProviderRejectionAndUnknownTimeoutStopWithoutReplay(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "429", true: "timeout"}[timeout], func(t *testing.T) {
			ctx, s, u, token, ids, api, h, delegate := wholeSetup(t)
			model := &schedulerModel{delegate: delegate, failure: &analysis.HTTPError{Status: 429}, timeout: timeout}
			api.ResumeModel = model
			runner := api.StartMatchTasks(ctx, 1)
			// Both branches must reach the provider. A 100ms whole-execution
			// budget could expire in SQL preparation under -race, turning the
			// intended 429 into a pre-dispatch timeout with zero model calls.
			// The timeout fixture blocks on ctx.Done, so a wider budget still
			// exercises a real in-provider deadline without a fixed sleep.
			runner.ExecutionTimeout = 2 * time.Second
			t.Cleanup(runner.Close)
			run := schedulerSubmit(t, ctx, s, u, token, ids, h)
			done := awaitRun(t, s, u, run.ID, "WAITING_AUTH")
			code := "MODEL_PROVIDER_BUSY"
			if timeout {
				code = "MODEL_TIMEOUT"
			}
			if done.Items[0].Code != code || done.ExecutionUntil != nil {
				t.Fatal(done)
			}
			time.Sleep(200 * time.Millisecond)
			if model.calls.Load() != 1 {
				t.Fatal("automatic paid replay", model.calls.Load())
			}
		})
	}
}

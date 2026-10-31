package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/source"
	"github.com/KanaDoodle/CampusTrace/internal/taskqueue"

	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

type Analyzer interface {
	Analyze(context.Context, d.Observation, string) ([]d.Claim, error)
}
type Worker struct {
	Source          source.DiscoveryAdapter
	radarCursor     string
	Version         string
	ParserVersion   string
	Store           *p.Store
	Queue           *Queue
	Analyzer        Analyzer
	MaxAttempts     int
	LaneConcurrency map[string]int
	Timeout, Lease  time.Duration
	TaskHistory     time.Duration
	Metrics         *observability.Metrics
}

func (w *Worker) Dispatch(ctx context.Context) error {
	_, err := w.dispatch(ctx)
	return err
}
func (w *Worker) dispatch(ctx context.Context) (int, error) {
	if _, e := w.Store.BackfillOutboxTenants(ctx); e != nil {
		return 0, e
	}
	return w.Store.FairOutbox(ctx, func(ctx context.Context, t p.Task) (bool, error) {
		if t.Type == "ANALYZE" {
			o, e := w.Store.Observation(ctx, t.EntityID)
			if e != nil && !errors.Is(e, p.ErrNotFound) {
				return false, e
			}
			// An orphan legacy envelope still reaches the normal terminal/DLQ
			// path. It must not roll back healthy owners' dispatch transactions.
			if e == nil {
				t, e = w.Store.BindAnalysis(ctx, t, w.processingVersion(o))
				if e != nil {
					return false, e
				}
			}
		}
		e := w.Queue.Publish(ctx, t)
		if errors.Is(e, ErrQueueFull) {
			if w.Metrics != nil {
				w.Metrics.Add("queue_admission_deferred_total", 1)
			}
			return false, nil
		}
		return e == nil, e
	})
}
func (w *Worker) Process(ctx context.Context, t p.Task) (err error) {
	if err := ValidateTask(t); err != nil {
		return err
	}
	ctx, span := observability.Consumer(ctx, "task.process", t.TraceParent)
	defer func() { observability.End(span, err) }()
	span.SetAttributes(attribute.String("task.type", t.Type), attribute.String("task.id", t.ID), attribute.Int("task.attempt", t.Attempt))
	fields := observability.From(ctx)
	fields.RequestID = t.CorrelationID
	fields.TaskID = t.ID
	ctx = observability.With(ctx, fields)
	if w.Metrics != nil {
		w.Metrics.Observe("task_age_seconds", max(0, time.Since(t.CreatedAt).Seconds()))
		started := time.Now()
		defer func() {
			w.Metrics.Since("task_process", started)
			if err != nil {
				w.Metrics.Add("task_errors_total", 1)
			}
		}()
	}
	if t.Type == "WATCH_CHECK" || t.Type == "WATCH_FETCH" {
		return w.processWatch(ctx, t)
	}
	if t.Type == "SOURCE_IMPORT" {
		return w.processSourceImport(ctx, t)
	}
	if t.Type == "ASSESS" {
		return w.Store.Assess(ctx, t.ID, t.EntityID)
	}
	o, err := w.Store.Observation(ctx, t.EntityID)
	if err != nil {
		return err
	}
	t, err = w.Store.BindAnalysis(ctx, t, w.processingVersion(o))
	if err != nil {
		return err
	}
	done, err := w.Store.Analyzed(ctx, t.EntityID, t.ProcessingVersion)
	if err != nil {
		return err
	}
	if done {
		return w.Store.ReconcileAnalysis(ctx, t.EntityID, t.ProcessingVersion, t.Generation)
	}
	if t.ProcessingVersion != w.processingVersion(o) {
		return p.ErrProcessingUnavailable
	}
	claims := []d.Claim{}
	if o.FetchStatus == "SUCCESS" {
		key := w.Queue.Prefix + "analysis:cache-v2:" + t.ProcessingVersion + ":" + d.Hash(o.Text)
		cached, e := w.Queue.R.Get(ctx, key).Bytes()
		cacheValid := e == nil
		if e == nil {
			if e = json.Unmarshal(cached, &claims); e != nil {
				cacheValid = false
			}
			for _, c := range claims {
				if c.Validate(o.Text) != nil {
					cacheValid = false
					break
				}
			}
		}
		// JSON null is a valid saved empty result, distinct from a cache miss.
		if !cacheValid {
			began, e := w.Store.BeginAnalysisInvocation(ctx, t)
			if e != nil {
				return e
			}
			if !began {
				return ErrAnalysisUncertain
			}
			start := time.Now()
			ctx = observability.With(ctx, observability.Fields{RequestID: t.CorrelationID, TaskID: t.ID, JobID: o.JobID})
			claims, err = w.Analyzer.Analyze(ctx, o, t.CorrelationID)
			w.Metrics.Since("analysis_latency", start)
			w.Metrics.Since("rpc_latency", start)
			if err != nil {
				w.Metrics.Add("rpc_errors", 1)
				// Only an explicit rejection permits a later automatic attempt.
				var rejected *analysis.HTTPError
				if errors.As(err, &rejected) && (rejected.Status == 429 || rejected.Status == 401 || rejected.Status == 403) {
					cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					e := w.Store.RejectAnalysisInvocation(cleanup, t)
					stop()
					if e != nil {
						return ErrAnalysisUncertain
					}
				} else {
					return ErrAnalysisUncertain
				}
				return err
			}
			if err = w.Queue.R.Set(ctx, key, d.JSON(claims), 24*time.Hour).Err(); err != nil {
				return ErrAnalysisUncertain
			}
		}
	}
	return w.Store.PersistAnalysis(ctx, o.ID, t.ProcessingVersion, claims)
}
func (w *Worker) handle(root, ctx context.Context, m taskqueue.Message) (taskqueue.Outcome, error) {
	t, e := Decode(m.Body)
	if e != nil {
		return taskqueue.Outcome{}, e
	}
	var err error
	if !t.QueueUntil.IsZero() && time.Now().After(t.QueueUntil) {
		err = ErrQueueExpired
	} else {
		w.Metrics.Observe("queue_wait_seconds_"+m.Lane, max(0, time.Since(t.CreatedAt).Seconds()))
		err = w.Process(ctx, t)
	}
	if root.Err() != nil {
		return taskqueue.Outcome{}, root.Err()
	}
	if errors.Is(err, context.Canceled) {
		return taskqueue.Outcome{}, err
	}
	// Settlement uses a fresh bounded context when the task deadline expires, but
	// the transport itself refuses settlement when its root process is canceled.
	persist, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	if err == nil {
		return taskqueue.Outcome{State: "DONE"}, nil
	}
	var sourceFailure *source.FetchError
	if (t.Type == "WATCH_CHECK" || t.Type == "WATCH_FETCH" || t.Type == "SOURCE_IMPORT") && errors.As(err, &sourceFailure) && sourceFailure.Category == "RATE_LIMIT" {
		return taskqueue.Outcome{State: "RETRY", Body: json.RawMessage(d.JSON(t)), Delay: time.Minute, Category: "SOURCE_PACING"}, nil
	}
	w.Metrics.Add("task_failures", 1)
	out := FailureOutcome(t, err, w.MaxAttempts)
	if t.Type == "ANALYZE" && !errors.Is(err, p.ErrProcessingUnavailable) && out.State == "DEAD" {
		code := "ANALYSIS_FAILED"
		if errors.Is(err, ErrQueueExpired) {
			code = "QUEUE_EXPIRED"
		}
		if errors.Is(err, ErrAnalysisUncertain) {
			code = "ANALYSIS_OUTCOME_UNKNOWN"
		}
		if e = w.Store.AnalysisTaskFailed(persist, t, code); e != nil {
			return taskqueue.Outcome{}, e
		}
	}
	if (t.Type == "WATCH_CHECK" || t.Type == "WATCH_FETCH") && out.State == "DEAD" {
		if e = w.terminalWatch(persist, t); e != nil {
			return taskqueue.Outcome{}, e
		}
		if e = w.Store.SourceImportWatchFailed(persist, t, sourceImportFailure(err)); e != nil {
			return taskqueue.Outcome{}, e
		}
	}
	if t.Type == "SOURCE_IMPORT" && out.State == "DEAD" {
		if e = w.Store.FailSourceImport(persist, t, sourceImportFailure(err)); e != nil {
			return taskqueue.Outcome{}, e
		}
	}

	slog.WarnContext(observability.Extract(ctx, t.TraceParent), "task failed", "task_id", t.ID, "category", out.Category, "retry", out.State == "RETRY")
	return out, nil
}
func (w *Worker) Run(ctx context.Context) (err error) {
	defer func() {
		if ctx.Err() != nil {
			err = nil
		}
	}()
	if w.Timeout <= 0 || w.Lease <= w.Timeout || w.MaxAttempts < 1 || w.MaxAttempts > MaxTaskAttempt {
		return errors.New("task timeout positive, attempts in 1..100, and execution lease must exceed task timeout")
	}
	retention := w.TaskHistory
	if retention == 0 {
		retention = 24 * time.Hour
	}
	if retention < time.Hour || retention > 365*24*time.Hour {
		return errors.New("task history retention must be between one hour and one year")
	}
	if err := w.Queue.Broker.SetLease(w.Lease); err != nil {
		return err
	}
	if err := w.Queue.Init(ctx); err != nil {
		return err
	}
	if err := w.Store.InitAssessmentSchedule(ctx); err != nil {
		return err
	}
	var wg sync.WaitGroup
	if w.Queue.ResolveOwner == nil {
		w.Queue.ResolveOwner = w.Store.TaskOwner
	}
	total := 0
	for _, lane := range QueueLanes() {
		n := w.LaneConcurrency[lane]
		if n == 0 {
			n = 1
		}
		if n < 1 || n > 16 {
			return errors.New("lane concurrency must be in 1..16")
		}
		total += n
	}
	if total > 64 {
		return errors.New("total worker concurrency exceeds 64")
	}
	for _, lane := range QueueLanes() {
		n := w.LaneConcurrency[lane]
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(lane string) {
				defer wg.Done()
				_ = w.Queue.Broker.Consume(ctx, lane, w.Timeout, func(task context.Context, m taskqueue.Message) (taskqueue.Outcome, error) {
					return w.handle(ctx, task, m)
				})
			}(lane)
		}
	}
	freshness := time.NewTimer(0)
	defer freshness.Stop()
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(time.Minute)
		defer tick.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			batch, cancel := context.WithTimeout(ctx, 30*time.Second)
			w.radarTick(batch)
			cancel()
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	delay := 200 * time.Millisecond
	dispatch := time.NewTimer(delay)
	defer dispatch.Stop()
	history := time.NewTimer(0)
	defer history.Stop()
	defer wg.Wait()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-freshness.C:
			n, err := w.Store.ScheduleAssessments(ctx, time.Now().UTC(), 100)
			if err != nil && ctx.Err() == nil {
				slog.Warn("freshness scheduling failed")
			}
			if err == nil && n > 0 && w.Metrics != nil {
				w.Metrics.Add("assessments_scheduled_total", float64(n))
			}
			next := time.Minute
			if err == nil && n == 100 {
				next = time.Second
			}
			freshness.Reset(next)
		case <-history.C:
			cleanup, cancel := context.WithTimeout(ctx, 3*time.Second)
			n, err := w.Queue.TrimAcknowledged(cleanup, retention)
			cancel()
			if err != nil && ctx.Err() == nil {
				slog.Warn("task history cleanup failed")
			} else if n > 0 && w.Metrics != nil {
				w.Metrics.Add("queue_history_trimmed_total", float64(n))
			}
			next := time.Minute
			if err == nil && n > 0 {
				next = time.Second
			}
			history.Reset(next)
		case <-dispatch.C:
			dispatched, dispatchErr := w.dispatch(ctx)
			if dispatchErr != nil && ctx.Err() == nil {
				slog.Warn("outbox dispatch failed")
			}
			scheduled, scheduleErr := w.Queue.Schedule(ctx)
			if scheduleErr != nil && ctx.Err() == nil {
				slog.Warn("retry scheduler failed")
			}
			delay = dispatchDelay(delay, dispatched+scheduled, dispatchErr != nil || scheduleErr != nil)
			dispatch.Reset(delay)
		}
	}
}

func (w *Worker) processingVersion(o d.Observation) string {
	parser := w.ParserVersion
	if parser == "" {
		parser = d.ParserVersion
	}
	return d.ProcessingVersion(w.Version, parser, o.ParserVersion)
}

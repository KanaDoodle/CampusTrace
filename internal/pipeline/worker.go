package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/redis/go-redis/v9"
	"log/slog"
	"sync"
	"time"
)

type Analyzer interface {
	Analyze(context.Context, d.Observation, string) ([]d.Claim, error)
}
type Worker struct {
	Version                  string
	ParserVersion            string
	Store                    *p.Store
	Queue                    *Queue
	Analyzer                 Analyzer
	Concurrency, MaxAttempts int
	Timeout, ClaimIdle       time.Duration
	Metrics                  *observability.Metrics
}

func (w *Worker) Dispatch(ctx context.Context) error {
	return w.Store.Tx(ctx, func(tx *sql.Tx) error {
		tasks, err := p.Many[p.Task](ctx, tx, "SELECT body FROM outbox WHERE sent=FALSE ORDER BY created_at LIMIT 50 FOR UPDATE SKIP LOCKED")
		if err != nil {
			return err
		}
		for _, t := range tasks {
			if t.Type == "ANALYZE" {
				o, e := w.Store.Observation(ctx, t.EntityID)
				if e != nil {
					return e
				}
				t, e = w.Store.BindAnalysis(ctx, t, w.processingVersion(o))
				if e != nil {
					return e
				}
			}
			if err = w.Queue.Publish(ctx, t); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE outbox SET sent=TRUE WHERE id=?", t.ID); err != nil {
				return err
			}
		}
		return nil
	})
}
func (w *Worker) Process(ctx context.Context, t p.Task) error {
	if err := ValidateTask(t); err != nil {
		return err
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
		if e == nil {
			if e = json.Unmarshal(cached, &claims); e != nil {
				claims = nil
			}
			for _, c := range claims {
				if c.Validate(o.Text) != nil {
					claims = nil
					break
				}
			}
		}
		if claims == nil || e != nil {
			start := time.Now()
			ctx = observability.With(ctx, observability.Fields{RequestID: t.CorrelationID, TaskID: t.ID, JobID: o.JobID})
			claims, err = w.Analyzer.Analyze(ctx, o, t.CorrelationID)
			w.Metrics.Since("analysis_latency", start)
			w.Metrics.Since("rpc_latency", start)
			if err != nil {
				w.Metrics.Add("rpc_errors", 1)
				return err
			}
			if err = w.Queue.R.Set(ctx, key, d.JSON(claims), 24*time.Hour).Err(); err != nil {
				return err
			}
		}
	}
	return w.Store.PersistAnalysis(ctx, o.ID, t.ProcessingVersion, claims)
}
func (w *Worker) handle(root context.Context, m redis.XMessage, consumers ...string) {
	t, err := Decode(m)
	if err != nil {
		consumer := "unknown"
		if len(consumers) > 0 {
			consumer = consumers[0]
		}
		ctx, cancel := context.WithTimeout(root, w.Timeout)
		defer cancel()
		if e := w.Queue.Quarantine(ctx, m, t, err, consumer); e != nil {
			slog.Error("poison quarantine failed", "message_id", m.ID)
		} else {
			w.Metrics.Add("poison_count", 1)
		}
		return
	}
	ctx, cancel := context.WithTimeout(root, w.Timeout)
	defer cancel()
	if err == nil {
		err = w.Process(ctx, t)
	}
	if root.Err() != nil {
		return
	}
	if err == nil {
		if e := w.Queue.Ack(ctx, m.ID); e != nil {
			slog.Error("task ack failed", "task_id", t.ID)
		} else {
			w.Metrics.Add("tasks_processed", 1)
			slog.InfoContext(ctx, "task completed", "task_id", t.ID, "observation_id", t.EntityID, "request_id", t.CorrelationID)
		}
		return
	}
	w.Metrics.Add("task_failures", 1)
	if t.ID == "" {
		t = p.NewTask("ANALYZE", "invalid-envelope")
		err = errors.New("schema: invalid envelope")
	}
	if t.Type == "ANALYZE" && !errors.Is(err, p.ErrProcessingUnavailable) && (!Transient(err) || t.Attempt >= w.MaxAttempts) {
		if e := w.Store.AnalysisTaskFailed(root, t, "ANALYSIS_FAILED"); e != nil {
			slog.Warn("could not persist terminal analysis failure", "task_id", t.ID)
			return
		}
	}
	retry, e := w.Queue.Fail(root, m.ID, t, err, w.MaxAttempts)
	if e != nil {
		slog.Error("task failure persistence failed", "task_id", t.ID)
	} else if retry {
		w.Metrics.Add("retry_count", 1)
	} else {
		w.Metrics.Add("dlq_count", 1)
	}
	slog.Warn("task failed", "task_id", t.ID, "observation_id", t.EntityID, "request_id", t.CorrelationID, "retry", retry, "category", func() string {
		if Transient(err) {
			return "TRANSIENT"
		}
		return "PERMANENT"
	}())
}
func (w *Worker) Run(ctx context.Context) error {
	if w.Concurrency < 1 || w.Timeout <= 0 || w.ClaimIdle <= w.Timeout || w.MaxAttempts < 1 || w.MaxAttempts > MaxTaskAttempt {
		return errors.New("workers/timeout positive, attempts in 1..100, and claim idle must exceed task timeout")
	}
	if err := w.Queue.Init(ctx); err != nil {
		return err
	}
	var wg sync.WaitGroup
	for i := 0; i < w.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			consumer := d.ID()
			for ctx.Err() == nil {
				msgs, err := w.Queue.Recover(ctx, consumer, w.ClaimIdle)
				if err == nil {
					for _, m := range msgs {
						w.handle(ctx, m, consumer)
					}
				}
				streams, err := w.Queue.R.XReadGroup(ctx, &redis.XReadGroupArgs{Group: w.Queue.Group(), Consumer: consumer, Streams: []string{w.Queue.Stream(), ">"}, Count: 1, Block: time.Second}).Result()
				if err == nil {
					for _, stream := range streams {
						for _, m := range stream.Messages {
							w.handle(ctx, m, consumer)
						}
					}
				} else if err != redis.Nil && ctx.Err() == nil {
					select {
					case <-ctx.Done():
					case <-time.After(200 * time.Millisecond):
					}
				}
			}
		}()
	}
	freshness := time.NewTicker(time.Minute)
	defer freshness.Stop()
	if err := w.Store.EnqueueFreshness(ctx); err != nil {
		slog.Warn("initial freshness scheduling failed")
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	defer wg.Wait()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-freshness.C:
			if err := w.Store.EnqueueFreshness(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("freshness scheduling failed")
			}
		case <-ticker.C:
			if err := w.Dispatch(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("outbox dispatch failed")
			}
			if _, err := w.Queue.Schedule(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("retry scheduler failed")
			}
		}
	}
}

func (w *Worker) version() string { return d.EffectiveAnalysisVersion(w.Version) }

func (w *Worker) processingVersion(o d.Observation) string {
	parser := w.ParserVersion
	if parser == "" {
		parser = d.ParserVersion
	}
	return d.ProcessingVersion(w.Version, parser, o.ParserVersion)
}

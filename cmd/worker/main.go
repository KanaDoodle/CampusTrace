package main

import (
	"context"
	"encoding/json"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	"github.com/KanaDoodle/CampusTrace/internal/bootstrap"
	"github.com/KanaDoodle/CampusTrace/internal/config"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/eventbus"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	"github.com/KanaDoodle/CampusTrace/internal/pipeline"
	"github.com/KanaDoodle/CampusTrace/internal/searchapi"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

func main() {
	ctx, cancel := bootstrap.Root()
	defer cancel()
	shutdown, err := observability.Setup(ctx, "campustrace-worker")
	if err != nil {
		slog.Error("tracing configuration failed")
		return
	}
	defer func() {
		flush, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = shutdown(flush)
	}()
	historyHours := config.Int("TASK_HISTORY_HOURS", 24)
	if historyHours > 365*24 {
		slog.Error("TASK_HISTORY_HOURS must be in 1..8760")
		return
	}
	app, err := bootstrap.Open(ctx)
	if err != nil {
		slog.Error("startup failed", "error", err)
		return
	}
	defer app.Close()
	if app.Store.Search != nil && len(os.Getenv("SEARCH_WRITE_TOKEN")) < 32 {
		slog.Error("worker requires SEARCH_WRITE_TOKEN for projection sync")
		return
	}
	if err = app.Store.Migrate(ctx); err != nil {
		slog.Error("worker database migration failed")
		return
	}
	if app.Store.JobEvents {
		cfg, e := eventbus.FromEnv("")
		if e != nil {
			slog.Error("invalid event configuration")
			return
		}
		producer, e := eventbus.NewProducer(cfg)
		if e != nil {
			slog.Error("event producer startup failed")
			return
		}
		defer producer.Close()
		if client, ok := app.Store.Search.(*searchapi.Client); ok {
			app.Store.Search = &eventbus.SearchTransport{HTTP: client, Producer: producer}
		}
		var publishWG sync.WaitGroup
		publishWG.Add(1)
		go func() { defer publishWG.Done(); app.Store.RunJobEventPublisher(ctx, producer) }()
		defer func() { cancel(); publishWG.Wait() }()
	}
	if app.Store.Search != nil {
		var syncWG sync.WaitGroup
		syncWG.Add(1)
		go func() { defer syncWG.Done(); app.Store.RunSearchSync(ctx) }()
		defer func() { cancel(); syncWG.Wait() }()
	}
	rpc, err := analysis.NewClient(app.Config.Endpoints(), app.Config.AnalysisConcurrency, app.Config.TaskTimeout)
	if err != nil {
		slog.Error("RPC startup failed", "error", err)
		return
	}
	defer rpc.Close()
	metricsMux := http.NewServeMux()
	metricsMux.Handle("GET /metrics", app.Metrics)
	metricsMux.HandleFunc("GET /queues", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		stats, e := app.Queue.Stats(ctx)
		if e != nil {
			http.Error(w, "queue stats unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	})
	metricsMux.HandleFunc("GET /metrics/prometheus", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		gauges := []observability.Gauge{}
		stats, e := app.Queue.Stats(ctx)
		up := 0.
		if e == nil {
			up = 1
			for _, lane := range stats {
				for name, value := range map[string]int64{"queue_ready": lane.Ready, "queue_inflight": lane.InFlight, "queue_outstanding": lane.Outstanding, "queue_capacity": lane.Capacity} {
					gauges = append(gauges, observability.Gauge{Name: name, Labels: map[string]string{"lane": lane.Lane}, Value: float64(value)})
				}
			}
		}
		gauges = append(gauges, observability.Gauge{Name: "queue_scrape_success", Value: up})
		app.Metrics.Prometheus(w, r, app.Store.DB.Stats(), gauges...)
	})
	metricsMux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	metricsServer := &http.Server{Addr: config.Env("WORKER_METRICS_ADDR", "127.0.0.1:18081"), Handler: metricsMux, ReadHeaderTimeout: 3 * time.Second}
	go func() {
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("worker metrics server failed", "error", err)
		}
	}()
	defer metricsServer.Close()
	w := pipeline.Worker{Version: config.Env("ANALYSIS_VERSION", d.AnalysisVersion), Store: app.Store, Queue: app.Queue, Analyzer: rpc, Concurrency: app.Config.Workers, MaxAttempts: app.Config.MaxAttempts, Timeout: app.Config.TaskTimeout, ClaimIdle: app.Config.ClaimIdle, TaskHistory: time.Duration(historyHours) * time.Hour, Metrics: app.Metrics}
	w.LaneConcurrency = map[string]int{}
	for _, lane := range pipeline.QueueLanes() {
		fallback := 1
		if lane == "ANALYZE" || lane == "WATCH_FETCH" {
			fallback = 2
		}
		w.LaneConcurrency[lane] = config.BoundedInt("WORKER_"+lane+"_CONCURRENCY", fallback, 16)
	}
	if err = w.Run(ctx); err != nil {
		slog.Error("worker stopped", "error", err)
	}
}

package main

import (
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	"github.com/KanaDoodle/CampusTrace/internal/bootstrap"
	"github.com/KanaDoodle/CampusTrace/internal/config"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/pipeline"
	"log/slog"
	"net/http"
	"time"
)

func main() {
	ctx, cancel := bootstrap.Root()
	defer cancel()
	app, err := bootstrap.Open(ctx)
	if err != nil {
		slog.Error("startup failed", "error", err)
		return
	}
	defer app.Close()
	rpc, err := analysis.NewClient(app.Config.Endpoints(), app.Config.AnalysisConcurrency, app.Config.TaskTimeout)
	if err != nil {
		slog.Error("RPC startup failed", "error", err)
		return
	}
	defer rpc.Close()
	metricsMux := http.NewServeMux()
	metricsMux.Handle("GET /metrics", app.Metrics)
	metricsMux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	metricsServer := &http.Server{Addr: config.Env("WORKER_METRICS_ADDR", "127.0.0.1:18081"), Handler: metricsMux, ReadHeaderTimeout: 3 * time.Second}
	go func() {
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("worker metrics server failed", "error", err)
		}
	}()
	defer metricsServer.Close()
	w := pipeline.Worker{Version: config.Env("ANALYSIS_VERSION", d.AnalysisVersion), Store: app.Store, Queue: app.Queue, Analyzer: rpc, Concurrency: app.Config.Workers, MaxAttempts: app.Config.MaxAttempts, Timeout: app.Config.TaskTimeout, ClaimIdle: app.Config.ClaimIdle, Metrics: app.Metrics}
	if err = w.Run(ctx); err != nil {
		slog.Error("worker stopped", "error", err)
	}
}

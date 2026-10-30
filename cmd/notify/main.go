// The notification consumer belongs to Core's domain but runs independently
// from the Redis task worker and the Search index consumer.
package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/config"
	"github.com/KanaDoodle/CampusTrace/internal/eventbus"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	shutdown, err := observability.Setup(ctx, "campustrace-notify")
	if err != nil {
		return err
	}
	defer func() {
		flush, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = shutdown(flush)
	}()
	store, err := p.Open(ctx, config.Load().DSN)
	if err != nil {
		return err
	}
	defer store.DB.Close()
	store.JobEvents = true
	if err = store.Migrate(ctx); err != nil {
		return err
	}
	metrics := observability.New()
	store.Metrics = metrics
	cfg, err := eventbus.FromEnv(config.Env("KAFKA_NOTIFY_GROUP", "campustrace-notify-v1"))
	if err != nil {
		return err
	}
	consumer, err := eventbus.NewConsumer(cfg, metrics)
	if err != nil {
		return err
	}
	defer consumer.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /metrics/prometheus", func(w http.ResponseWriter, r *http.Request) { metrics.Prometheus(w, r, sql.DBStats{}) })
	server := &http.Server{Addr: config.Env("NOTIFY_METRICS_ADDR", "127.0.0.1:18084"), Handler: mux, ReadHeaderTimeout: 3 * time.Second}
	defer server.Close()
	go func() {
		if e := server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			slog.Error("notification metrics failed")
			cancel()
		}
	}()
	return consumer.Run(ctx, store.ConsumeNotificationEvents)
}
func main() {
	slog.SetDefault(slog.New(observability.ContextHandler{Handler: slog.NewJSONHandler(os.Stderr, nil)}))
	if err := run(); err != nil {
		slog.Error("notification consumer stopped")
		os.Exit(1)
	}
}

// Search owns only its derived OpenSearch index. It cannot open Core's database.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/eventbus"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	"github.com/KanaDoodle/CampusTrace/internal/searchapi"
	"github.com/KanaDoodle/CampusTrace/internal/searchservice"
)

func env(key, fallback string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return fallback
}
func run() error {
	if len(os.Args) > 1 && os.Args[1] == "probe" {
		client := http.Client{Timeout: 3 * time.Second}
		r, e := client.Get("http://127.0.0.1:18083/readyz")
		if e != nil {
			return fmt.Errorf("search not ready")
		}
		defer r.Body.Close()
		if r.StatusCode != 200 {
			return fmt.Errorf("search not ready")
		}
		return nil
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if len(os.Args) > 1 && os.Args[1] == "rebuild" {
		client, e := searchapi.NewClient(env("SEARCH_URL", "http://127.0.0.1:18083"), os.Getenv("SEARCH_QUERY_TOKEN"), os.Getenv("SEARCH_WRITE_TOKEN"))
		if e != nil {
			return e
		}
		status, e := client.Rebuild(ctx)
		if e == nil {
			fmt.Println("Search index replaced; Core will replay job documents:", status.Generation)
		}
		return e
	}
	if len(os.Args) > 1 {
		return fmt.Errorf("usage: search [probe|rebuild]")
	}
	shutdown, err := observability.Setup(ctx, "campustrace-search")
	if err != nil {
		return err
	}
	defer func() {
		flush, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = shutdown(flush)
	}()
	backend, err := searchservice.NewBackend(env("OPENSEARCH_URL", "http://127.0.0.1:9200"), env("SEARCH_INDEX_ALIAS", "campustrace-jobs-v1"))
	if err != nil {
		return err
	}
	service, err := searchservice.NewServer(backend, os.Getenv("SEARCH_QUERY_TOKEN"), os.Getenv("SEARCH_WRITE_TOKEN"))
	if err != nil {
		return err
	}
	if os.Getenv("KAFKA_BROKERS") != "" {
		cfg, e := eventbus.FromEnv(env("KAFKA_SEARCH_GROUP", "campustrace-search-v1"))
		if e != nil {
			return e
		}
		consumer, e := eventbus.NewConsumer(cfg, service.Metrics)
		if e != nil {
			return e
		}
		defer consumer.Close()
		var consumerWG sync.WaitGroup
		consumerWG.Add(1)
		go func() { defer consumerWG.Done(); _ = consumer.Run(ctx, service.ConsumeEvents) }()
		defer func() { cancel(); consumerWG.Wait() }()
	}
	server := &http.Server{Addr: env("SEARCH_ADDR", "127.0.0.1:18083"), Handler: service.Handler(), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() {
		<-ctx.Done()
		stop, done := context.WithTimeout(context.Background(), 8*time.Second)
		defer done()
		_ = server.Shutdown(stop)
	}()
	slog.Info("search service started")
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("search server stopped")
	}
	return nil
}
func main() {
	slog.SetDefault(slog.New(observability.ContextHandler{Handler: slog.NewJSONHandler(os.Stderr, nil)}))
	if err := run(); err != nil {
		slog.Error("search failed", "reason", err)
		os.Exit(1)
	}
}

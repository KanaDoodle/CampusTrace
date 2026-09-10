package main

import (
	"context"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	"github.com/KanaDoodle/CampusTrace/internal/bootstrap"
	"github.com/KanaDoodle/CampusTrace/internal/config"
	"github.com/KanaDoodle/CampusTrace/internal/pipeline"
	"github.com/redis/go-redis/v9"
	"log/slog"
	"net/http"
	"time"
)

func main() {
	ctx, cancel := bootstrap.Root()
	defer cancel()
	c := config.Load()
	var extractor analysis.Extractor = analysis.RuleExtractor{}
	if c.LLMURL != "" {
		r := redis.NewClient(&redis.Options{Addr: c.Redis, ContextTimeoutEnabled: true})
		defer r.Close()
		q := pipeline.Queue{R: r, Prefix: "ct:"}
		client := analysis.NewChat(c.LLMURL, c.LLMKey, c.LLMModel, c.LLMConcurrency)
		client.Allow = func(ctx context.Context) (bool, error) { return q.Allow(ctx, "llm:global", 30, time.Minute) }
		extractor = analysis.LLMExtractor{Client: client}
	}
	srv, err := analysis.StartServer(ctx, c.Endpoints(), config.Env("ANALYSIS_ADDR", "127.0.0.1:19091"), config.Env("ANALYSIS_ADVERTISE", ""), config.Env("INSTANCE_ID", "analysis-1"), extractor, c.AnalysisConcurrency, 0)
	if err != nil {
		slog.Error("analysis startup failed", "error", err)
		return
	}
	defer srv.Close()
	health := &http.Server{Addr: config.Env("ANALYSIS_HEALTH_ADDR", "127.0.0.1:19191"), Handler: srv.HealthHandler(), ReadHeaderTimeout: 3 * time.Second}
	go func() {
		if err := health.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("analysis health listener failed", "error", err)
			cancel()
		}
	}()
	defer health.Close()

	select {
	case <-ctx.Done():
	case err := <-srv.Done:
		slog.Error("analysis stopped", "error", err)
	}
}

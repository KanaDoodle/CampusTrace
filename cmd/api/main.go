package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	"github.com/KanaDoodle/CampusTrace/internal/bootstrap"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	if err := run(); err != nil {
		slog.Error("CampusTrace API stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := bootstrap.Root()
	defer cancel()
	app, err := bootstrap.Open(ctx)
	if err != nil {
		return err
	}
	defer app.Close()
	if len(app.Config.JWT) < 32 {
		return errors.New("JWT_SECRET must contain 32+ bytes")
	}
	// Only cmd/seed applied migrations before, so a database volume created by
	// an older release kept an outdated schema while the API served Job Radar
	// routes: every query failed and surfaced as a bare 400. Migrate is
	// serialized by MySQL GET_LOCK and restartable, so the API can apply it.
	if err := app.Store.Migrate(ctx); err != nil {
		return fmt.Errorf("database migration failed; inspect database configuration and logs before restarting: %w", err)
	}
	slog.Info("database schema ready")
	slots := make(chan struct{}, app.Config.LLMConcurrency)
	if configured, ok := app.ResumeModel.(*analysis.ChatClient); ok {
		slots = configured.Sem
	}
	a := &transport.API{Store: app.Store, Queue: app.Queue, Tools: app.Tools, Agent: app.Agent, Auth: auth.Service{Store: app.Store, Secret: []byte(app.Config.JWT)}, Metrics: app.Metrics, ResumeModel: app.ResumeModel, ResumeModelName: app.Config.LLMModel, CustomModelSlots: slots}
	srv := &http.Server{Addr: app.Config.HTTP, Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()
	slog.Info("CampusTrace API listening", "address", srv.Addr)
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		shutdown, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer stop()
		srv.Shutdown(shutdown)
	}
	return nil
}

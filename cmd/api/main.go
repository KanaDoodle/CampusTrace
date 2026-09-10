package main

import (
	"context"
	"errors"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	"github.com/KanaDoodle/CampusTrace/internal/bootstrap"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
	"log/slog"
	"net"
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
	if len(app.Config.JWT) < 32 {
		slog.Error("JWT_SECRET must contain 32+ bytes")
		return
	}
	a := &transport.API{Store: app.Store, Queue: app.Queue, Tools: app.Tools, Agent: app.Agent, Auth: auth.Service{Store: app.Store, Secret: []byte(app.Config.JWT)}, Metrics: app.Metrics}
	srv := &http.Server{Addr: app.Config.HTTP, Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()
	slog.Info("CampusTrace API listening", "address", srv.Addr)
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http stopped", "error", err)
		}
	case <-ctx.Done():
		shutdown, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer stop()
		srv.Shutdown(shutdown)
	}
}

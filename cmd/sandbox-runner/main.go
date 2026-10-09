package main

import (
	"context"
	"encoding/json"
	"github.com/KanaDoodle/CampusTrace/internal/bootstrap"
	"github.com/KanaDoodle/CampusTrace/internal/config"
	"github.com/KanaDoodle/CampusTrace/internal/practice"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	key := []byte(os.Getenv("PRACTICE_SECRET"))
	if len(key) < 32 {
		slog.Error("PRACTICE_SECRET must be 32+ bytes")
		os.Exit(1)
	}
	ctx, cancel := bootstrap.Root()
	defer cancel()
	engine := practice.NewDocker(config.Env("PRACTICE_DOCKER_SOCKET", "/var/run/docker.sock"), config.Env("PRACTICE_IMAGE", practice.Image), config.Env("PRACTICE_OWNER", "campustrace-local"))
	cleanup, stop := context.WithTimeout(ctx, 10*time.Second)
	err := engine.Cleanup(cleanup)
	stop()
	if err != nil {
		slog.Error("practice cleanup failed")
		os.Exit(1)
	}
	slots := make(chan struct{}, 2)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		probe, done := context.WithTimeout(r.Context(), time.Second)
		defer done()
		if engine.Ready(probe) != nil {
			http.Error(w, "image unavailable", 503)
			return
		}
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if !practice.Authorize(r, key, nil) {
			http.Error(w, "unauthorized", 401)
			return
		}
		probe, done := context.WithTimeout(r.Context(), time.Second)
		defer done()
		if engine.Ready(probe) != nil {
			http.Error(w, "image unavailable", 503)
			return
		}
		w.Write([]byte(`{"ready":true}`))
	})
	mux.HandleFunc("POST /run", func(w http.ResponseWriter, r *http.Request) {
		b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 100000))
		if e != nil || !practice.Authorize(r, key, b) {
			http.Error(w, "unauthorized", 401)
			return
		}
		var v practice.Request
		if json.Unmarshal(b, &v) != nil || practice.Validate(v.Code, v.Tests) != nil || len(v.ID) != 32 {
			http.Error(w, "invalid exercise", 400)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "busy", 429)
			return
		}
		out, e := engine.Run(r.Context(), v)
		if e != nil {
			out.State = "ERROR"
			out.Output = "练习执行器暂时不可用，请检查练习组件。"
			slog.Error("practice run failed", "run_id", v.ID)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	})
	srv := &http.Server{Addr: config.Env("PRACTICE_ADDR", "0.0.0.0:18082"), Handler: mux, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() {
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		srv.Shutdown(shutdown)
	}()
	if e := srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		slog.Error("practice runner stopped", "error", e)
		os.Exit(1)
	}
}

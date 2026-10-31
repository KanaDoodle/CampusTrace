package searchservice

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/observability"
	a "github.com/KanaDoodle/CampusTrace/internal/searchapi"
)

type Engine interface {
	a.Transport
	Rebuild(context.Context) (a.Status, error)
}
type Server struct {
	Engine                 Engine
	QueryToken, WriteToken string
	Metrics                *observability.Metrics
	slots                  chan struct{}
}

func NewServer(engine Engine, queryToken, writeToken string) (*Server, error) {
	if len(queryToken) < 32 || len(writeToken) < 32 || queryToken == writeToken {
		return nil, a.ErrInvalid
	}
	return &Server{Engine: engine, QueryToken: queryToken, WriteToken: writeToken, Metrics: observability.New(), slots: make(chan struct{}, 16)}, nil
}
func decode(r *http.Request, v any) error {
	raw, e := io.ReadAll(io.LimitReader(r.Body, (5<<20)+1))
	if e != nil || len(raw) > 5<<20 {
		return a.ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		return a.ErrInvalid
	}
	return nil
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if _, e := s.Engine.Status(ctx); e != nil {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte("ready\n"))
	})
	mux.HandleFunc("GET /metrics/prometheus", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		ready := 0.
		if _, e := s.Engine.Status(ctx); e == nil {
			ready = 1
		}
		s.Metrics.Prometheus(w, r, sql.DBStats{}, observability.Gauge{Name: "search_index_ready", Value: ready})
	})
	on := func(pattern string, write bool, fn func(context.Context, *http.Request) (any, error)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			expected := s.QueryToken
			if write {
				expected = s.WriteToken
			}
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+expected)) != 1 {
				http.Error(w, "unauthorized", 401)
				return
			}
			select {
			case s.slots <- struct{}{}:
				defer func() { <-s.slots }()
			default:
				w.Header().Set("Retry-After", "1")
				http.Error(w, "busy", 503)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
			defer cancel()
			ctx, span := observability.Server(observability.HTTPContext(ctx, r.Header), pattern)
			started := time.Now()
			var out any
			var e error
			out, e = fn(ctx, r)
			observability.End(span, e)
			s.Metrics.Observe("search_request_seconds", time.Since(started).Seconds())
			s.Metrics.Add("search_requests_total", 1)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			if e != nil {
				code, status := "SEARCH_UNAVAILABLE", 503
				if errors.Is(e, a.ErrInvalid) {
					code, status = "SEARCH_INVALID", 400
				}
				if errors.Is(e, a.ErrConflict) {
					code, status = "SEARCH_CONFLICT", 409
				}
				s.Metrics.Add("search_errors_total", 1)
				w.WriteHeader(status)
				json.NewEncoder(w).Encode(map[string]string{"code": code})
				return
			}
			json.NewEncoder(w).Encode(out)
		})
	}
	on("GET /v1/status", false, func(ctx context.Context, r *http.Request) (any, error) { return s.Engine.Status(ctx) })
	on("POST /v1/query", false, func(ctx context.Context, r *http.Request) (any, error) {
		var q a.Query
		if decode(r, &q) != nil {
			return nil, a.ErrInvalid
		}
		return s.Engine.Query(ctx, q)
	})
	on("POST /v1/applied", false, func(ctx context.Context, r *http.Request) (any, error) {
		var q a.AppliedQuery
		if decode(r, &q) != nil {
			return nil, a.ErrInvalid
		}
		engine, ok := s.Engine.(interface {
			Applied(context.Context, a.AppliedQuery) (a.AppliedResult, error)
		})
		if !ok {
			return nil, a.ErrUnavailable
		}
		return engine.Applied(ctx, q)
	})
	on("POST /v1/batch", true, func(ctx context.Context, r *http.Request) (any, error) {
		var b a.Batch
		if decode(r, &b) != nil {
			return nil, a.ErrInvalid
		}
		if e := s.Engine.Apply(ctx, b); e != nil {
			return nil, e
		}
		return a.Status{Protocol: a.Protocol, Generation: b.Generation}, nil
	})
	on("POST /v1/rebuild", true, func(ctx context.Context, r *http.Request) (any, error) {
		var in struct{}
		if decode(r, &in) != nil {
			return nil, a.ErrInvalid
		}
		return s.Engine.Rebuild(ctx)
	})

	on("POST /v1/rebuild/claim", true, func(ctx context.Context, r *http.Request) (any, error) {
		var in a.RebuildRef
		if decode(r, &in) != nil {
			return nil, a.ErrInvalid
		}
		engine, ok := s.Engine.(a.OnlineRebuilder)
		if !ok {
			return nil, a.ErrUnavailable
		}
		return engine.ClaimRebuild(ctx, in)
	})
	on("POST /v1/rebuild/commit", true, func(ctx context.Context, r *http.Request) (any, error) {
		var in a.RebuildCommit
		if decode(r, &in) != nil {
			return nil, a.ErrInvalid
		}
		engine, ok := s.Engine.(a.OnlineRebuilder)
		if !ok {
			return nil, a.ErrUnavailable
		}
		return engine.CommitRebuild(ctx, in)
	})
	on("POST /v1/rebuild/ack", true, func(ctx context.Context, r *http.Request) (any, error) {
		var in a.RebuildRef
		if decode(r, &in) != nil {
			return nil, a.ErrInvalid
		}
		engine, ok := s.Engine.(a.OnlineRebuilder)
		if !ok {
			return nil, a.ErrUnavailable
		}
		return engine.AckRebuild(ctx, in)
	})
	on("POST /v1/rebuild/abort", true, func(ctx context.Context, r *http.Request) (any, error) {
		var in a.RebuildRef
		if decode(r, &in) != nil {
			return nil, a.ErrInvalid
		}
		engine, ok := s.Engine.(interface {
			AbortRebuild(context.Context, a.RebuildRef) (a.Status, error)
		})
		if !ok {
			return nil, a.ErrUnavailable
		}
		return engine.AbortRebuild(ctx, in)
	})
	on("POST /v1/rebuild/rollback", true, func(ctx context.Context, r *http.Request) (any, error) {
		var in struct{}
		if decode(r, &in) != nil {
			return nil, a.ErrInvalid
		}
		engine, ok := s.Engine.(interface {
			Rollback(context.Context) (a.Status, error)
		})
		if !ok {
			return nil, a.ErrUnavailable
		}
		return engine.Rollback(ctx)
	})
	return mux
}

package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/pipeline"
	"github.com/KanaDoodle/CampusTrace/internal/rag"
	"github.com/KanaDoodle/CampusTrace/internal/source"
	"github.com/KanaDoodle/CampusTrace/web"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type API struct {
	Store        *p.Store
	Queue        *pipeline.Queue
	Auth         auth.Service
	Tools        *agent.Tools
	Agent        *agent.Runtime
	Metrics      *observability.Metrics
	SourceLimits map[string]int
}

func write(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, p.ErrNotFound) {
			status = 404
		}
		if errors.Is(err, p.ErrConflict) || errors.Is(err, p.ErrStaleInput) {
			status = 409
		}
		if errors.Is(err, p.ErrBackendUnavailable) {
			status = 503
		}
		if errors.Is(err, rag.ErrCapacity) {
			w.WriteHeader(409)
			json.NewEncoder(w).Encode(map[string]string{"error": "CORPUS_CAPACITY", "code": "CORPUS_CAPACITY"})
			return
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": http.StatusText(status)})
		return
	}
	json.NewEncoder(w).Encode(v)
}
func decode(r *http.Request, v any) error {
	b, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	if err != nil {
		return err
	}
	return d.Strict(b, v)
}

type userKey struct{}

func user(r *http.Request) string { v, _ := r.Context().Value(userKey{}).(string); return v }
func (a *API) protected(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		id, err := a.Auth.Verify(token)
		if err != nil {
			w.WriteHeader(401)
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), userKey{}, id)))
	}
}
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.FS(web.Files)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { write(w, map[string]string{"status": "ok"}, nil) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if a.Store.DB.PingContext(ctx) != nil || a.Queue.R.Ping(ctx).Err() != nil {
			w.WriteHeader(503)
			return
		}
		write(w, map[string]string{"status": "ready"}, nil)
	})
	mux.Handle("GET /metrics", a.Metrics)
	for _, route := range []string{"register", "login"} {
		mux.HandleFunc("POST /auth/"+route, func(w http.ResponseWriter, r *http.Request) {
			var v struct {
				Email    string `json:"email"`
				Password string `json:"password"`
			}
			if err := decode(r, &v); err != nil {
				write(w, nil, err)
				return
			}
			ok, err := a.Queue.Allow(r.Context(), "auth:"+d.Hash(d.Normalize(v.Email)), 10, time.Minute)
			if err != nil || !ok {
				w.WriteHeader(429)
				return
			}
			if route == "register" {
				id, err := a.Auth.Register(r.Context(), v.Email, v.Password)
				write(w, map[string]string{"user_id": id}, err)
			} else {
				token, err := a.Auth.Login(r.Context(), v.Email, v.Password)
				if err != nil {
					w.WriteHeader(401)
					return
				}
				write(w, map[string]string{"token": token}, nil)
			}
		})
	}
	on := func(pattern string, h http.HandlerFunc) { mux.HandleFunc(pattern, a.protected(h)) }
	on("GET /api/jobs", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Store.JobsForUser(r.Context(), user(r), r.URL.Query().Get("q"))
		write(w, v, err)
	})
	on("GET /api/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		j, err := a.Store.JobForUser(r.Context(), user(r), id)
		if err != nil {
			write(w, nil, err)
			return
		}
		os, err := a.Store.Observations(r.Context(), id)
		if err != nil {
			write(w, nil, err)
			return
		}
		es, err := a.Store.Evidence(r.Context(), id)
		if err != nil {
			write(w, nil, err)
			return
		}
		as, err := a.Store.Assessments(r.Context(), id)
		if err != nil {
			write(w, nil, err)
			return
		}
		changes, err := a.Store.Changes(r.Context(), id)
		if err != nil {
			write(w, nil, err)
			return
		}
		e, rank, fit, eErr := a.Store.Evaluate(r.Context(), user(r), id)
		result := map[string]any{"job": j, "observations": os, "evidence": es, "assessments": as, "changes": changes, "go_fit": fit}
		if eErr == nil {
			result["eligibility"] = e
			result["ranking"] = rank
		} else {
			result["eligibility_notice"] = "Create a candidate profile to evaluate"
		}
		write(w, result, nil)
	})
	on("POST /api/ingest", func(w http.ResponseWriter, r *http.Request) {
		var i p.Ingest
		if err := decode(r, &i); err != nil {
			write(w, nil, err)
			return
		}
		i.SourceID = "manual"
		if i.FetchStatus == "" {
			i.FetchStatus = "SUCCESS"
		}
		if i.Text == "" && i.URL != "" {
			limit := 6
			if n := a.SourceLimits[i.SourceID]; n > 0 {
				limit = n
			}
			ok, err := a.Queue.Allow(r.Context(), "source:"+i.SourceID, limit, time.Minute)
			if err != nil || !ok {
				w.WriteHeader(429)
				return
			}
			res, err := (source.HTTPAdapter{Client: source.PublicClient()}).Fetch(r.Context(), i.URL)
			if err != nil {
				write(w, nil, err)
				return
			}
			i.Text, i.FetchStatus, i.HTTPStatus = res.Text, res.Status, res.HTTPStatus
		}
		v, err := a.Store.IngestForUser(r.Context(), user(r), i)
		write(w, v, err)
	})
	on("POST /api/import", func(w http.ResponseWriter, r *http.Request) {
		var rows []p.Ingest
		var err error
		if strings.Contains(r.Header.Get("Content-Type"), "text/csv") {
			rows, err = source.CSV(r.Body)
		} else {
			rows, err = source.JSON(r.Body)
		}
		if err != nil {
			write(w, nil, err)
			return
		}
		results := []any{}
		for _, i := range rows {
			i.SourceID = "manual"
			if i.Text == "" && i.URL != "" {
				ok, err := a.Queue.Allow(r.Context(), "source:manual", 6, time.Minute)
				if err != nil || !ok {
					results = append(results, map[string]string{"error": "source rate limited"})
					continue
				}
				res, err := (source.HTTPAdapter{Client: source.PublicClient()}).Fetch(r.Context(), i.URL)
				if err != nil {
					results = append(results, map[string]string{"error": "invalid public URL"})
					continue
				}
				i.Text, i.FetchStatus, i.HTTPStatus = res.Text, res.Status, res.HTTPStatus
			}
			v, err := a.Store.IngestForUser(r.Context(), user(r), i)
			if err != nil {
				results = append(results, map[string]string{"error": "import row failed"})
			} else {
				results = append(results, v)
			}
		}
		write(w, results, nil)
	})
	on("GET /api/profile", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Store.Profile(r.Context(), user(r))
		write(w, v, err)
	})
	on("PUT /api/profile", func(w http.ResponseWriter, r *http.Request) {
		var v d.Profile
		if err := decode(r, &v); err != nil {
			write(w, nil, err)
			return
		}
		write(w, map[string]bool{"saved": true}, a.Store.SaveProfile(r.Context(), user(r), v))
	})
	for _, table := range []string{"applications", "interviews", "reviews", "weak_topics", "projects", "project_facts", "documents"} {
		on("GET /api/"+table, func(w http.ResponseWriter, r *http.Request) {
			v, err := a.Store.Owned(r.Context(), table, user(r))
			write(w, v, err)
		})
	}
	on("GET /api/applications/{id}/history", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Store.ApplicationHistory(r.Context(), user(r), r.PathValue("id"))
		write(w, v, err)
	})
	on("POST /api/applications", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(io.LimitReader(r.Body, 65537))
		if err != nil {
			write(w, nil, err)
			return
		}
		v, err := a.Store.ApplyAction(r.Context(), user(r), d.ID(), "create_application", b)
		write(w, v, err)
	})
	on("POST /api/applications/transition", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(io.LimitReader(r.Body, 65537))
		if err != nil {
			write(w, nil, err)
			return
		}
		v, err := a.Store.ApplyAction(r.Context(), user(r), d.ID(), "transition_application", b)
		write(w, v, err)
	})
	on("POST /api/interviews", func(w http.ResponseWriter, r *http.Request) {
		var v d.Interview
		if err := decode(r, &v); err != nil {
			write(w, nil, err)
			return
		}
		out, err := a.Store.SaveInterview(r.Context(), user(r), v)
		write(w, out, err)
	})
	on("POST /api/interviews/{id}/finish", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Result string `json:"result"`
			Notes  string `json:"notes"`
		}
		if err := decode(r, &v); err != nil {
			write(w, nil, err)
			return
		}
		out, err := a.Store.FinishInterview(r.Context(), user(r), r.PathValue("id"), v.Result, v.Notes)
		write(w, out, err)
	})
	on("POST /api/reviews", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(io.LimitReader(r.Body, 65537))
		if err != nil {
			write(w, nil, err)
			return
		}
		v, err := a.Store.ApplyAction(r.Context(), user(r), d.ID(), "record_interview_review", b)
		write(w, v, err)
	})
	on("POST /api/projects", func(w http.ResponseWriter, r *http.Request) {
		var v d.Project
		if err := decode(r, &v); err != nil {
			write(w, nil, err)
			return
		}
		out, err := a.Store.SaveProject(r.Context(), user(r), v)
		write(w, out, err)
	})
	on("POST /api/project_facts", func(w http.ResponseWriter, r *http.Request) {
		var v d.ProjectFact
		if err := decode(r, &v); err != nil {
			write(w, nil, err)
			return
		}
		out, err := a.Store.SaveFact(r.Context(), user(r), v)
		write(w, out, err)
	})
	on("POST /api/documents", func(w http.ResponseWriter, r *http.Request) {
		var v rag.Document
		if err := decode(r, &v); err != nil {
			write(w, nil, err)
			return
		}
		out, err := a.Tools.RAG.Ingest(r.Context(), user(r), v)
		write(w, out, err)
	})
	on("GET /api/knowledge", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Tools.RAG.Search(r.Context(), user(r), r.URL.Query().Get("q"), 5)
		write(w, v, err)
	})
	on("GET /api/jobs/{id}/preparation", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Tools.Prepare(r.Context(), user(r), r.PathValue("id"))
		write(w, v, err)
	})
	for _, stream := range []bool{false, true} {
		path := "POST /agent/decide"
		if stream {
			path = "POST /agent/stream"
		}
		on(path, func(w http.ResponseWriter, r *http.Request) {
			var v struct {
				Session string `json:"session_id"`
				Message string `json:"message"`
			}
			if err := decode(r, &v); err != nil || v.Session == "" || v.Message == "" || len(v.Message) > 4000 || len(v.Session) > 64 {
				write(w, nil, p.ErrValidation)
				return
			}
			start := time.Now()
			defer a.Metrics.Since("agent_latency", start)
			if !stream {
				result := a.Agent.Run(r.Context(), user(r), v.Session, v.Message, nil)
				write(w, result, nil)
				return
			}
			SSE(w, r, func(ctx context.Context, emit func(agent.Event) error) {
				a.Agent.Run(ctx, user(r), v.Session, v.Message, emit)
			})
		})
	}
	on("POST /agent/actions/{id}/confirm", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Confirm bool `json:"confirm"`
		}
		if err := decode(r, &v); err != nil || !v.Confirm {
			write(w, nil, p.ErrValidation)
			return
		}
		out, err := a.Tools.Confirm(r.Context(), user(r), r.PathValue("id"))
		write(w, out, err)
	})
	on("GET /agent/traces/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Queue.R.Get(r.Context(), a.Queue.Prefix+"agent:trace:"+user(r)+":"+r.PathValue("id")).Result()
		if err != nil {
			write(w, nil, p.ErrNotFound)
			return
		}
		write(w, json.RawMessage(v), nil)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := d.ID()
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		slog.InfoContext(r.Context(), "http request", "request_id", id, "method", r.Method, "path", r.URL.Path)
		mux.ServeHTTP(w, r.WithContext(observability.With(r.Context(), observability.Fields{RequestID: id})))
	})
}
func SSE(w http.ResponseWriter, r *http.Request, run func(context.Context, func(agent.Event) error)) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	rc := http.NewResponseController(w)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	run(ctx, func(e agent.Event) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		rc.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, d.JSON(e.Data))
		if err == nil {
			err = rc.Flush()
		}
		if err != nil {
			cancel()
		}
		return err
	})
}

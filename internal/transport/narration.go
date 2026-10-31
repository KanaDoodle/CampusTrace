package transport

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/agent"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func (a *API) narrationRoutes(on func(string, http.HandlerFunc)) {
	on("GET /api/agent/narration/capabilities", func(w http.ResponseWriter, r *http.Request) {
		result := map[string]any{"enabled": false}
		if a.Narrations != nil && a.Narrations.Client != nil {
			result["enabled"] = true
			result["model"] = a.Narrations.Client.Label()
			result["evidence_kind"] = a.Narrations.Client.EvidenceKind()
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, result, nil)
	})
	on("POST /api/agent/executions/{id}/narration/preview", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			MaskName string `json:"mask_name"`
		}
		if decode(r, &input) != nil {
			write(w, nil, p.ErrValidation)
			return
		}
		if a.Narrations == nil {
			write(w, nil, p.ErrBackendUnavailable)
			return
		}
		v, err := a.Narrations.Preview(r.Context(), user(r), r.PathValue("id"), input.MaskName)
		w.Header().Set("Cache-Control", "no-store")
		write(w, v, err)
	})
	for _, path := range []string{"POST /api/agent/executions/{id}/narration", "POST /api/agent/executions/{id}/narration/resume"} {
		on(path, func(w http.ResponseWriter, r *http.Request) {
			var input agent.NarrationRequest
			if decode(r, &input) != nil {
				write(w, nil, p.ErrValidation)
				return
			}
			if a.Narrations == nil {
				write(w, nil, p.ErrBackendUnavailable)
				return
			}
			var v p.AgentExecution
			var err error
			if r.Pattern == "POST /api/agent/executions/{id}/narration" {
				v, err = a.Narrations.Start(r.Context(), user(r), r.PathValue("id"), input)
			} else {
				v, err = a.Narrations.Resume(r.Context(), user(r), r.PathValue("id"), input)
			}
			w.Header().Set("Cache-Control", "no-store")
			write(w, executionResponse(v), err)
		})
	}
	on("GET /api/agent/executions/{id}/stream", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Store.AgentExecution(r.Context(), user(r), r.PathValue("id"))
		if err != nil {
			write(w, nil, err)
			return
		}
		if v.SkillID != p.NarrationSkillID {
			write(w, nil, p.ErrValidation)
			return
		}
		// This is a read-only snapshot subscription, never a resume operation.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		controller := http.NewResponseController(w)
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		last := r.Header.Get("Last-Event-ID")
		lastWrite := time.Now()
		for {
			// Long-lived reads must observe logout/session expiry as well as owner.
			if !a.narrationObserverAuthorized(ctx, r) {
				return
			}
			revision := uint64(0)
			if v.Narration != nil {
				revision = v.Narration.Revision
			}
			cursor := fmt.Sprintf("%s/%d/%d/%s/%t", v.ID, v.Resumes, revision, v.State, v.CanResume)
			if cursor != last {
				if controller.SetWriteDeadline(time.Now().Add(5*time.Second)) != nil {
					return
				}
				if _, err := fmt.Fprintf(w, "id: %s\nevent: snapshot\ndata: %s\n\n", cursor, d.JSON(executionResponse(v))); err != nil {
					return
				}
				if controller.Flush() != nil {
					return
				}
				last = cursor
				lastWrite = time.Now()
			}
			if v.State != "RUNNING" || v.CanResume {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			v, err = a.Store.AgentExecution(ctx, user(r), v.ID)
			if err != nil {
				return
			}
			if time.Since(lastWrite) > 5*time.Second {
				_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
					return
				}
				if controller.Flush() != nil {
					return
				}
				lastWrite = time.Now()
			}
		}
	})
}

func (a *API) narrationObserverAuthorized(ctx context.Context, r *http.Request) bool {
	var id string
	var err error
	if header := r.Header.Get("Authorization"); header != "" {
		id, err = a.Auth.Verify(strings.TrimPrefix(header, "Bearer "))
	} else if cookie, e := r.Cookie(sessionCookie); e == nil {
		id, err = a.sessions().Verify(ctx, cookie.Value)
	} else {
		return false
	}
	return err == nil && id != "" && id == user(r)
}

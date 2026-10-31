package transport

import (
	"context"
	"errors"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"net/http"
	"time"
)

func (a *API) modelCallRoutes(on func(string, http.HandlerFunc)) {
	on("GET /api/matching/calls", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if a.ModelControl == nil {
			write(w, map[string]any{"enabled": false, "calls": []p.ModelCall{}}, nil)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		calls, e := a.Store.ModelCalls(ctx, user(r), a.ModelControl.Options.Policy.ID)
		if e != nil {
			write(w, nil, p.ErrBackendUnavailable)
			return
		}
		pool, e := a.Store.ModelPoolStatus(ctx, a.ModelControl.Options.Policy.ID)
		if e != nil {
			write(w, nil, p.ErrBackendUnavailable)
			return
		}
		write(w, map[string]any{"enabled": true, "calls": calls, "pool": pool}, nil)
	})
	on("POST /api/matching/calls/{id}/review", func(w http.ResponseWriter, r *http.Request) {
		if a.ModelControl == nil {
			codedError(w, 503, "MATCH_RUN_UNAVAILABLE")
			return
		}
		var in p.ModelCallReview
		if e := decode(r, &in); e != nil {
			write(w, nil, e)
			return
		}
		out, e := a.Store.ReviewModelCall(r.Context(), user(r), a.ModelControl.Options.Policy.ID, r.PathValue("id"), in)
		if e != nil && !errors.Is(e, p.ErrConflict) && !errors.Is(e, p.ErrNotFound) && !errors.Is(e, p.ErrValidation) {
			e = p.ErrBackendUnavailable
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, out, e)
	})
}

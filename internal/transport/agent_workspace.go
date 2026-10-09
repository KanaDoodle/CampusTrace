package transport

import (
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"net/http"
)

func (a *API) agentWorkspaceRoutes(on func(string, http.HandlerFunc)) {
	a.agentHarnessRoutes(on)
	on("GET /api/agent/memories", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		v, e := a.Store.AgentMemories(r.Context(), user(r))
		write(w, v, e)
	})
	on("POST /api/agent/memories", func(w http.ResponseWriter, r *http.Request) {
		var v p.MemoryInput
		if e := decode(r, &v); e != nil {
			write(w, nil, p.ErrValidation)
			return
		}
		out, e := a.Store.SaveMemory(r.Context(), user(r), v)
		write(w, out, e)
	})
	on("DELETE /api/agent/memories/{id}", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Version uint64 `json:"version"`
		}
		if e := decode(r, &v); e != nil || v.Version == 0 {
			write(w, nil, p.ErrValidation)
			return
		}
		e := a.Store.ForgetMemory(r.Context(), user(r), r.PathValue("id"), v.Version)
		if e == nil && a.Queue != nil {
			// Remove old short-session contexts too. SQL revision already blocks any in-flight late save.
			var cursor uint64
			for {
				keys, next, err := a.Queue.R.Scan(r.Context(), cursor, a.Queue.Prefix+"agent:session:"+user(r)+":*", 100).Result()
				if err != nil {
					break
				}
				if len(keys) > 0 {
					a.Queue.R.Unlink(r.Context(), keys...)
				}
				cursor = next
				if cursor == 0 {
					break
				}
			}
		}
		write(w, map[string]bool{"forgotten": e == nil}, e)
	})
	on("GET /api/agent/tasks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		v, e := a.Store.AgentTasks(r.Context(), user(r))
		write(w, v, e)
	})
	on("DELETE /api/agent/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		out, e := a.Store.DB.ExecContext(r.Context(), "DELETE FROM agent_tasks WHERE id=? AND user_id=?", r.PathValue("id"), user(r))
		if e == nil {
			n, _ := out.RowsAffected()
			if n == 0 {
				e = p.ErrNotFound
			}
		}
		write(w, map[string]bool{"deleted": e == nil}, e)
	})
}

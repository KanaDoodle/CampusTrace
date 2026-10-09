package transport

import (
	"context"
	"net/http"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/mcpclient"
	"github.com/KanaDoodle/CampusTrace/internal/modelconfig"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func (a *API) agentHarnessRoutes(on func(string, http.HandlerFunc)) {
	on("GET /api/agent/skills", func(w http.ResponseWriter, r *http.Request) { write(w, agent.Skills(), nil) })
	on("GET /api/agent/executions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		v, e := a.Store.AgentExecutions(r.Context(), user(r))
		write(w, v, e)
	})
	on("GET /api/agent/executions/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		v, e := a.Store.AgentExecution(r.Context(), user(r), r.PathValue("id"))
		write(w, v, e)
	})
	on("POST /api/agent/executions", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			agent.SkillInput
			MaskName string              `json:"mask_name"`
			Model    *modelconfig.Config `json:"model_config,omitempty"`
		}
		if decode(r, &v) != nil || len(v.MaskName) > 200 {
			write(w, nil, p.ErrValidation)
			return
		}
		tools := *a.Tools
		tools.MaskName = v.MaskName
		identity := matchPreviewRequest{MaskName: v.MaskName}
		if v.Model != nil {
			if v.Model.Validate() != nil {
				write(w, nil, p.ErrValidation)
				return
			}
			identity.ModelURL = v.Model.URL
			identity.ModelName = v.Model.Model
		}
		model, e := a.matchIdentity(identity)
		if e != nil {
			write(w, nil, e)
			return
		}
		tools.MatchModel = model
		out, e := (agent.SkillRunner{Store: a.Store, Tools: &tools}).Start(r.Context(), user(r), v.SkillInput)
		write(w, out, e)
	})
	on("POST /api/agent/executions/{id}/resume", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			MaskName string              `json:"mask_name"`
			Model    *modelconfig.Config `json:"model_config,omitempty"`
		}
		if decode(r, &v) != nil || len(v.MaskName) > 200 {
			write(w, nil, p.ErrValidation)
			return
		}
		tools := *a.Tools
		tools.MaskName = v.MaskName
		identity := matchPreviewRequest{MaskName: v.MaskName}
		if v.Model != nil {
			if v.Model.Validate() != nil {
				write(w, nil, p.ErrValidation)
				return
			}
			identity.ModelURL = v.Model.URL
			identity.ModelName = v.Model.Model
		}
		model, e := a.matchIdentity(identity)
		if e != nil {
			write(w, nil, e)
			return
		}
		tools.MatchModel = model
		out, e := (agent.SkillRunner{Store: a.Store, Tools: &tools}).Resume(r.Context(), user(r), r.PathValue("id"))
		write(w, out, e)
	})
	on("POST /api/agent/executions/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		e := a.Store.CancelAgentExecution(r.Context(), user(r), r.PathValue("id"))
		write(w, map[string]bool{"cancelled": e == nil}, e)
	})
	on("GET /api/agent/todos", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if e := a.Store.RefreshAgentFeed(r.Context(), user(r)); e != nil {
			write(w, nil, e)
			return
		}
		v, e := a.Store.AgentTodos(r.Context(), user(r))
		write(w, v, e)
	})
	on("POST /api/agent/todos/settings", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Enabled bool `json:"enabled"`
		}
		if decode(r, &v) != nil {
			write(w, nil, p.ErrValidation)
			return
		}
		e := a.Store.SetAgentFeed(r.Context(), user(r), v.Enabled)
		write(w, map[string]bool{"enabled": v.Enabled}, e)
	})
	on("POST /api/agent/todos/{id}/dismiss", func(w http.ResponseWriter, r *http.Request) {
		e := a.Store.DismissAgentTodo(r.Context(), user(r), r.PathValue("id"))
		write(w, map[string]bool{"dismissed": e == nil}, e)
	})
	on("GET /api/agent/connectors", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		v, e := a.Store.AgentConnectors(r.Context(), user(r))
		write(w, v, e)
	})
	on("POST /api/agent/connectors/discover", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			mcpclient.Connector
			Token string `json:"token"`
		}
		if decode(r, &v) != nil || v.Validate() != nil {
			write(w, nil, p.ErrValidation)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		out, e := a.Tools.MCP.Discover(ctx, v.Connector, v.Token)
		if e != nil {
			codedError(w, http.StatusBadRequest, "MCP_UNAVAILABLE")
			return
		}
		write(w, out, nil)
	})
	on("POST /api/agent/connectors", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			mcpclient.Connector
			Token string `json:"token"`
		}
		if decode(r, &v) != nil || v.Validate() != nil || len(v.Allowed) == 0 {
			write(w, nil, p.ErrValidation)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		resources, e := a.Tools.MCP.Discover(ctx, v.Connector, v.Token)
		if e != nil {
			codedError(w, http.StatusBadRequest, "MCP_UNAVAILABLE")
			return
		}
		available := map[string]bool{}
		names := map[string]string{}
		for _, r := range resources {
			available[r.URI] = true
			names[r.URI] = r.Name
		}
		v.Names = map[string]string{}
		for _, uri := range v.Allowed {
			if !available[uri] {
				write(w, nil, p.ErrValidation)
				return
			}
			v.Names[uri] = names[uri]
		}
		out, e := a.Store.SaveAgentConnector(r.Context(), user(r), v.Connector)
		write(w, out, e)
	})
	on("DELETE /api/agent/connectors/{id}", func(w http.ResponseWriter, r *http.Request) {
		r0, e := a.Store.DB.ExecContext(r.Context(), "DELETE FROM agent_connectors WHERE id=? AND user_id=?", r.PathValue("id"), user(r))
		if e == nil {
			n, _ := r0.RowsAffected()
			if n == 0 {
				e = p.ErrNotFound
			}
		}
		write(w, map[string]bool{"deleted": e == nil}, e)
	})
}

// A durable cursor turns source/profile events into bounded local reminders.
// No model call, message delivery, application write, or analysis retry occurs.
func (a *API) StartAgentFeed(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				tick, cancel := context.WithTimeout(ctx, 20*time.Second)
				e := a.Store.RefreshEnabledAgentFeeds(tick)
				cancel()
				if e != nil && ctx.Err() == nil && a.Metrics != nil {
					a.Metrics.Add("agent_feed_errors_total", 1)
				}
			}
		}
	}()
}

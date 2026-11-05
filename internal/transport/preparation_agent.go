package transport

import (
	"errors"
	"net/http"

	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/modelconfig"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/projectdefense"
	"github.com/KanaDoodle/CampusTrace/internal/rag"
)

type preparationAPIRequest struct {
	agent.PreparationRequest
	Model    *modelconfig.Config `json:"model_config,omitempty"`
	MaskName string              `json:"mask_name,omitempty"`
	Budget   int                 `json:"token_budget,omitempty"`
}

func (a *API) preparationEnvironment(in preparationAPIRequest) (agent.PreparationEnvironment, error) {
	var client *analysis.ChatClient
	var err error
	if in.Model != nil {
		client, err = a.customModel(*in.Model)
	} else {
		client, _ = a.ResumeModel.(*analysis.ChatClient)
	}
	if err != nil {
		return agent.PreparationEnvironment{}, err
	}
	if client == nil || client.URL == "" || client.Model == "" {
		return agent.PreparationEnvironment{}, p.ErrBackendUnavailable
	}
	tools := *a.Tools
	tools.MaskName = in.MaskName
	return agent.PreparationEnvironment{Model: agent.LiveModel{Client: client}, ModelIdentity: d.Hash(client.URL + "\x00" + client.Model), ModelLabel: client.Model, EvidenceKind: "model-api", MaskName: in.MaskName, Tools: &tools, Budget: in.Budget}, nil
}
func (a *API) preparationAgentRoutes(on func(string, http.HandlerFunc)) {
	on("GET /api/agent/preparation/materials", func(w http.ResponseWriter, r *http.Request) {
		// Use local retrieval regardless of optional provider settings.
		retrieval, err := a.requestRAG(rag.Options{})
		if err != nil {
			knowledgeWrite(w, nil, err)
			return
		}
		tools := agent.Tools{Store: a.Store, RAG: retrieval}
		v, err := tools.PrepareApplication(r.Context(), user(r), r.URL.Query().Get("application_id"))
		knowledgeWrite(w, v, err)
	})
	on("GET /api/agent/preparation/defense-memories", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		rows, err := a.Store.DefenseMemories(r.Context(), user(r), r.URL.Query().Get("topic"))
		write(w, rows, err)
	})
	on("POST /api/agent/preparation/{id}/memory-preview", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var input struct {
			Note     string `json:"note"`
			Disputed bool   `json:"disputed"`
			Days     int    `json:"days"`
		}
		if decode(r, &input) != nil {
			write(w, nil, p.ErrValidation)
			return
		}
		v, err := a.Store.AgentExecution(r.Context(), user(r), r.PathValue("id"))
		if err != nil {
			write(w, nil, err)
			return
		}
		if v.State != "COMPLETED" || v.Preparation == nil || v.Preparation.Defense == nil || v.Preparation.Defense.Review == nil {
			write(w, nil, p.ErrConflict)
			return
		}
		s := v.Preparation
		args := p.SaveDefenseMemoryArgs{ExecutionID: v.ID, Revision: s.Revision, ReviewHash: d.Hash(d.JSON(s.Defense.Review)), EvidenceVersion: s.Defense.Version, Note: input.Note, Disputed: input.Disputed, Days: input.Days}
		a.defenseMemoryProposal(w, r, args)
	})
	on("POST /api/agent/preparation/defense-memories/{id}/preview", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Version  uint64 `json:"version"`
			Note     string `json:"note"`
			Disputed bool   `json:"disputed"`
			Days     int    `json:"days"`
		}
		if decode(r, &input) != nil {
			write(w, nil, p.ErrValidation)
			return
		}
		old, err := a.Store.DefenseMemory(r.Context(), user(r), r.PathValue("id"))
		if err != nil {
			write(w, nil, err)
			return
		}
		if old.Version != input.Version || old.Episode == nil {
			write(w, nil, p.ErrConflict)
			return
		}
		e := old.Episode
		args := p.SaveDefenseMemoryArgs{ID: old.ID, Version: input.Version, ExecutionID: e.ExecutionID, Revision: e.Revision, ReviewHash: d.Hash(d.JSON(e.Review)), EvidenceVersion: e.EvidenceVersion, Note: input.Note, Disputed: input.Disputed, Days: input.Days}
		a.defenseMemoryProposal(w, r, args)
	})
	on("GET /api/agent/preparation/defense-topics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		write(w, map[string]any{"version": projectdefense.Version(), "catalog": projectdefense.Load()}, nil)
	})
	for _, path := range []string{"POST /api/agent/preparation/preview", "POST /api/agent/preparation", "POST /api/agent/preparation/{id}/resume"} {
		on(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			var input preparationAPIRequest
			if decode(r, &input) != nil || a.Preparations == nil {
				write(w, nil, p.ErrValidation)
				return
			}
			env, err := a.preparationEnvironment(input)
			if err != nil {
				write(w, nil, err)
				return
			}
			switch r.Pattern {
			case "POST /api/agent/preparation/preview":
				v, e := a.Preparations.Preview(r.Context(), user(r), input.Input, env)
				write(w, v, e)
			case "POST /api/agent/preparation":
				v, e := a.Preparations.Start(r.Context(), user(r), input.PreparationRequest, env)
				writePreparation(w, executionResponse(v), e)
			default:
				v, e := a.Preparations.Resume(r.Context(), user(r), r.PathValue("id"), input.PreparationRequest, env)
				writePreparation(w, executionResponse(v), e)
			}
		})
	}
	on("GET /api/agent/preparation", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		rows, err := a.Store.PreparationExecutions(r.Context(), user(r), r.URL.Query().Get("application_id"))
		write(w, rows, err)
	})
	on("POST /api/agent/preparation/{id}/save-preview", func(w http.ResponseWriter, r *http.Request) {
		if a.Preparations == nil {
			write(w, nil, p.ErrBackendUnavailable)
			return
		}
		args, err := a.Preparations.SavePreview(r.Context(), user(r), r.PathValue("id"))
		if err != nil {
			write(w, nil, err)
			return
		}
		pending, err := a.Tools.Propose(r.Context(), user(r), "save_preparation_plan", []byte(d.JSON(args)))
		write(w, pending, err)
	})
}

func (a *API) defenseMemoryProposal(w http.ResponseWriter, r *http.Request, args p.SaveDefenseMemoryArgs) {
	w.Header().Set("Cache-Control", "no-store")
	preview, err := a.Store.PreviewDefenseMemory(r.Context(), user(r), args)
	if err != nil {
		write(w, nil, err)
		return
	}
	pending, err := a.Tools.Propose(r.Context(), user(r), "remember_defense_memory", []byte(d.JSON(args)))
	write(w, map[string]any{"pending": pending, "memory": preview}, err)
}

func writePreparation(w http.ResponseWriter, v any, err error) {
	if errors.Is(err, p.ErrBackendUnavailable) {
		codedError(w, http.StatusServiceUnavailable, "PREPARATION_SERVICE_UNAVAILABLE")
		return
	}
	write(w, v, err)
}

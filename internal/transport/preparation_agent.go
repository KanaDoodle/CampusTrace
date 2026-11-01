package transport

import (
	"errors"
	"net/http"

	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/modelconfig"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
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

func writePreparation(w http.ResponseWriter, v any, err error) {
	if errors.Is(err, p.ErrBackendUnavailable) {
		codedError(w, http.StatusServiceUnavailable, "PREPARATION_SERVICE_UNAVAILABLE")
		return
	}
	write(w, v, err)
}

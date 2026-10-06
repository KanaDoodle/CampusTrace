package transport

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	"github.com/KanaDoodle/CampusTrace/internal/modelconfig"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/resume"
)

type matchPreviewRequest struct {
	ModelURL  string `json:"model_url"`
	ModelName string `json:"model_name"`
	MaskName  string `json:"mask_name"`
}

func (a *API) matchIdentity(in matchPreviewRequest) (string, error) {
	if len(in.ModelURL) > 2048 || len(in.ModelName) > 128 || len(in.MaskName) > 100 || (in.MaskName != "" && len([]rune(strings.TrimSpace(in.MaskName))) < 2) {
		return "", p.ErrValidation
	}
	if in.ModelURL != "" && in.ModelName != "" {
		return matching.ModelIdentity(in.ModelURL, in.ModelName), nil
	}
	if client, ok := a.ResumeModel.(*analysis.ChatClient); ok {
		return matching.ModelIdentity(client.URL, client.Model), nil
	}
	return matching.ModelIdentity("server-default", a.ResumeModelName), nil
}
func matchFailure(w http.ResponseWriter, err error, stage ...string) {
	status, code := 502, ""
	switch {
	case errors.Is(err, p.ErrMatchJobUnavailable):
		status, code = 409, "MATCH_JOB_UNAVAILABLE"
	case errors.Is(err, p.ErrMatchQuota):
		status, code = 429, "MATCH_DAILY_LIMIT"
	case errors.Is(err, p.ErrStaleInput):
		status, code = 409, "MATCH_INPUT_CHANGED"
	case errors.Is(err, matching.ErrDecisionCapacity):
		status, code = 400, "MATCH_DECISION_CAPACITY"
	case errors.Is(err, matching.ErrCapacity):
		status, code = 400, "MATCH_CAPACITY"
	case errors.Is(err, matching.ErrInvalid):
		code = "MATCH_OUTPUT_INVALID"
	default:
		if errors.Is(err, p.ErrNotFound) || errors.Is(err, p.ErrValidation) || errors.Is(err, p.ErrBackendUnavailable) {
			write(w, nil, err)
			return
		}
		code = resumeDraftFailure(err)
	}
	phase := "PREPARE"
	if len(stage) > 0 && (stage[0] == "EXTRACT" || stage[0] == "COMPARE" || stage[0] == "SAVE") {
		phase = stage[0]
	}
	diagnostic := map[string]any{"stage": phase}
	addCapacityDiagnostic(diagnostic, err)
	var validation *matching.ValidationError
	if errors.As(err, &validation) {
		diagnostic["validation_reason"] = validation.Reason
		if validation.JobIndex > 0 {
			diagnostic["job_index"] = validation.JobIndex
		}
		if validation.ItemIndex > 0 {
			diagnostic["item_index"] = validation.ItemIndex
		}
		if validation.Reason == "MATCH_COUNT" || validation.Reason == "JOB_COUNT" {
			diagnostic["expected"] = validation.Expected
			diagnostic["actual"] = validation.Actual
		}
	}
	var provider *analysis.HTTPError
	if errors.As(err, &provider) {
		diagnostic["provider_status"] = provider.Status
	}
	var response *analysis.ResponseError
	if errors.As(err, &response) {
		diagnostic["response_reason"] = response.Reason
	}
	requestID := w.Header().Get("X-Request-ID")
	// Do not log err.Error(): it may embed URLs, provider text or user input.
	slog.Warn("matching failed", "request_id", requestID, "code", code, "diagnostic", diagnostic)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": code, "code": code, "request_id": requestID, "diagnostic": diagnostic})
}

func addCapacityDiagnostic(diagnostic map[string]any, err error) {
	var capacity *matching.CapacityError
	if errors.As(err, &capacity) {
		diagnostic["capacity_reason"] = capacity.Reason
		diagnostic["actual"] = capacity.Actual
		diagnostic["limit"] = capacity.Limit
	}
}

type budgetedMatchModel struct {
	model resume.Completer
	store *p.Store
	user  string
	calls int
}

func (m *budgetedMatchModel) Complete(ctx context.Context, messages, tools any) (json.RawMessage, error) {
	return m.CompleteJSON(ctx, messages, tools)
}
func (m *budgetedMatchModel) CompleteJSON(ctx context.Context, messages, tools any) (json.RawMessage, error) {
	if err := m.store.CheckMatchRunGuard(ctx, m.user); err != nil {
		return nil, err
	}
	if err := m.store.ReserveMatchCall(ctx, m.user, time.Now()); err != nil {
		return nil, err
	}
	m.calls++
	if model, ok := m.model.(interface {
		CompleteJSON(context.Context, any, any) (json.RawMessage, error)
	}); ok {
		return model.CompleteJSON(ctx, messages, tools)
	}
	return m.model.Complete(ctx, messages, tools)
}

func (a *API) matchingRoutes(on func(string, http.HandlerFunc)) {
	on("POST /api/matching/preview", func(w http.ResponseWriter, r *http.Request) {
		var in matchPreviewRequest
		if err := decode(r, &in); err != nil {
			write(w, nil, err)
			return
		}
		identity, err := a.matchIdentity(in)
		if err != nil {
			write(w, nil, err)
			return
		}
		v, err := a.Store.MatchSnapshot(r.Context(), user(r), identity, strings.TrimSpace(in.MaskName), nil)
		if err != nil {
			if errors.Is(err, p.ErrNotFound) {
				codedError(w, 409, "MATCH_PROFILE_REQUIRED")
			} else {
				matchFailure(w, err)
			}
			return
		}
		write(w, v, nil)
	})
	on("PUT /api/matching/settings", func(w http.ResponseWriter, r *http.Request) {
		var in matching.Settings
		if err := decode(r, &in); err != nil {
			write(w, nil, err)
			return
		}
		write(w, in, a.Store.SaveMatchSettings(r.Context(), user(r), in))
	})
	on("POST /api/matching/results/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in matchPreviewRequest
		if err := decode(r, &in); err != nil {
			write(w, nil, err)
			return
		}
		identity, err := a.matchIdentity(in)
		if err != nil {
			write(w, nil, err)
			return
		}
		id := r.PathValue("id")
		snapshot, err := a.Store.MatchDecisionSnapshot(r.Context(), user(r), identity, strings.TrimSpace(in.MaskName), []string{id}, "")
		if err != nil {
			matchFailure(w, err)
			return
		}
		result := snapshot.Jobs[0].Result
		if result == nil {
			write(w, map[string]any{"state": snapshot.Jobs[0].State, "local": snapshot.Jobs[0].Local, "excluded_reason": snapshot.Jobs[0].ExcludedReason}, nil)
			return
		}
		write(w, map[string]any{"state": snapshot.Jobs[0].State, "local": snapshot.Jobs[0].Local, "excluded_reason": snapshot.Jobs[0].ExcludedReason, "result": result}, nil)
	})
	on("POST /api/matching/analyze", a.analyzeMatches)
	on("POST /api/matching/export", a.exportMatches)
	on("POST /api/matching/import/{action}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("action") != "preview" && r.PathValue("action") != "confirm" {
			codedError(w, 404, "NOT_FOUND")
			return
		}
		a.importChatMatches(w, r)
	})
	on("POST /api/matching/company", a.compareCompany)
	on("POST /api/matching/preparation/{id}", a.prepareMatchedJob)
}

func (a *API) analyzeMatches(w http.ResponseWriter, r *http.Request) {
	var in struct {
		JobIDs        []string            `json:"job_ids"`
		CandidateHash string              `json:"candidate_hash"`
		MaskName      string              `json:"mask_name"`
		Model         *modelconfig.Config `json:"model_config,omitempty"`
	}
	if err := decode(r, &in); err != nil || len(in.JobIDs) == 0 || len(in.JobIDs) > matching.MaxBatch {
		codedError(w, 400, "MATCH_CAPACITY")
		return
	}
	identity, err := a.matchIdentity(matchPreviewRequest{MaskName: in.MaskName})
	if err != nil {
		write(w, nil, err)
		return
	}
	model := a.ResumeModel
	if in.Model != nil {
		client, err := a.customModel(*in.Model)
		if err != nil {
			codedError(w, 400, "MODEL_CONFIG_INVALID")
			return
		}
		client.OutputTokenLimit = 12000
		model = resumeModelWithTimeout(client)
		identity = matching.ModelIdentity(in.Model.URL, in.Model.Model)
	} else if client, ok := model.(*analysis.ChatClient); ok {
		copyClient := *client
		copyClient.OutputTokenLimit = 12000
		model = resumeModelWithTimeout(&copyClient)
	}
	if model == nil {
		codedError(w, 503, "RESUME_MODEL_UNAVAILABLE")
		return
	}
	if a.Queue == nil {
		write(w, nil, p.ErrBackendUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 175*time.Second)
	defer cancel()
	// One active matching batch per account prevents duplicate paid work across
	// tabs. The lease outlives this request's timeout, and releases by token.
	lockKey := a.Queue.Prefix + "matching:lease:" + user(r)
	lease := d.ID()
	acquired, err := a.Queue.R.SetNX(ctx, lockKey, lease, 4*time.Minute).Result()
	if err != nil {
		write(w, nil, p.ErrBackendUnavailable)
		return
	}
	if !acquired {
		codedError(w, 409, "MATCH_BUSY")
		return
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer done()
		a.Queue.R.Eval(cleanup, `if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) end return 0`, []string{lockKey}, lease)
	}()
	runs, err := a.Store.MatchRuns(ctx, user(r))
	if err != nil {
		write(w, nil, err)
		return
	}
	for _, run := range runs {
		if run.State == "RUNNING" || run.State == "PAUSING" {
			codedError(w, 409, "MATCH_BUSY")
			return
		}
	}
	out, err := a.executeMatchBatch(ctx, user(r), matchBatchInput{in.JobIDs, in.CandidateHash, in.MaskName}, model, identity, nil)
	if err != nil {
		var stage *MatchStageError
		if errors.As(err, &stage) {
			matchFailure(w, stage.Err, stage.Stage)
		} else {
			matchFailure(w, err)
		}
		return
	}
	write(w, out, nil)
}

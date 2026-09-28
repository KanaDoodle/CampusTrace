package transport

import (
	"context"
	"encoding/json"
	"errors"
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
func matchFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, p.ErrMatchQuota):
		codedError(w, 429, "MATCH_DAILY_LIMIT")
	case errors.Is(err, p.ErrStaleInput):
		codedError(w, 409, "MATCH_INPUT_CHANGED")
	case errors.Is(err, matching.ErrCapacity):
		codedError(w, 400, "MATCH_CAPACITY")
	case errors.Is(err, matching.ErrInvalid):
		codedError(w, 502, "MATCH_OUTPUT_INVALID")
	default:
		if errors.Is(err, p.ErrNotFound) || errors.Is(err, p.ErrValidation) || errors.Is(err, p.ErrBackendUnavailable) {
			write(w, nil, err)
			return
		}
		codedError(w, 502, resumeDraftFailure(err))
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
		snapshot, err := a.Store.MatchSnapshot(r.Context(), user(r), identity, strings.TrimSpace(in.MaskName), []string{id})
		if err != nil {
			matchFailure(w, err)
			return
		}
		result, err := a.Store.MatchResult(r.Context(), user(r), id)
		if errors.Is(err, p.ErrNotFound) {
			write(w, map[string]any{"state": snapshot.Jobs[0].State}, nil)
			return
		}
		write(w, map[string]any{"state": snapshot.Jobs[0].State, "result": result}, err)
	})
	on("POST /api/matching/analyze", a.analyzeMatches)
	on("POST /api/matching/export", a.exportMatches)
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
		client.OutputTokenLimit = 8000
		model = resumeModelWithTimeout(client)
		identity = matching.ModelIdentity(in.Model.URL, in.Model.Model)
	} else if client, ok := model.(*analysis.ChatClient); ok {
		copyClient := *client
		copyClient.OutputTokenLimit = 8000
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
	snapshot, err := a.Store.MatchSnapshot(ctx, user(r), identity, strings.TrimSpace(in.MaskName), in.JobIDs)
	if err != nil {
		matchFailure(w, err)
		return
	}
	if in.CandidateHash != snapshot.CandidateHash {
		matchFailure(w, p.ErrStaleInput)
		return
	}
	remaining := []p.MatchJob{}
	reused := []string{}
	total := 0
	for _, job := range snapshot.Jobs {
		if job.State == "ANALYZED" {
			reused = append(reused, job.Job.ID)
			continue
		}
		if job.ExcludedReason != "" {
			codedError(w, 409, "MATCH_JOB_UNAVAILABLE")
			return
		}
		if job.TextBytes == 0 || job.TextBytes > matching.MaxBatchText {
			matchFailure(w, matching.ErrCapacity)
			return
		}
		total += job.TextBytes
		remaining = append(remaining, job)
	}
	if total > matching.MaxBatchText {
		matchFailure(w, matching.ErrCapacity)
		return
	}
	budget := &budgetedMatchModel{model: model, store: a.Store, user: user(r)}
	requirements := map[string]matching.Requirements{}
	uncached := []matching.JobText{}
	representative := map[string]string{}
	requirementsReused := 0
	for _, job := range remaining {
		reqs, err := a.Store.CachedRequirements(ctx, user(r), job.RequirementsKey)
		if err == nil {
			requirements[job.Job.ID] = reqs
			requirementsReused++
			continue
		}
		if !errors.Is(err, p.ErrNotFound) {
			matchFailure(w, err)
			return
		}
		if representative[job.RequirementsKey] == "" {
			representative[job.RequirementsKey] = job.Job.ID
			uncached = append(uncached, matching.JobText{ID: job.Job.ID, Text: job.Text})
		}
	}
	if len(uncached) > 0 {
		extracted, err := matching.Extract(ctx, budget, uncached)
		if err != nil {
			matchFailure(w, err)
			return
		}
		for _, job := range remaining {
			if _, ok := requirements[job.Job.ID]; ok {
				continue
			}
			reqs := extracted[representative[job.RequirementsKey]]
			requirements[job.Job.ID] = reqs
			if err := a.Store.SaveRequirements(ctx, user(r), job.RequirementsKey, reqs); err != nil {
				matchFailure(w, err)
				return
			}
		}
	}
	inputs := []matching.MatchInput{}
	for _, job := range remaining {
		if len(requirements[job.Job.ID].Items) > 0 {
			inputs = append(inputs, matching.MatchInput{ID: job.Job.ID, Requirements: requirements[job.Job.ID].Items})
		}
	}
	analyzed := []string{}
	byID := map[string]p.MatchJob{}
	for _, job := range remaining {
		byID[job.Job.ID] = job
	}
	persist := func(job p.MatchJob, matches []matching.Match) error {
		reqs := requirements[job.Job.ID].Items
		if matches == nil {
			matches = []matching.Match{}
		}
		score, coverage := matching.Score(reqs, matches)
		result := matching.Result{JobID: job.Job.ID, InputKey: job.InputKey, RequirementsKey: job.RequirementsKey, CandidateHash: snapshot.CandidateHash, Model: identity, AnalyzedAt: time.Now().UTC(), Requirements: reqs, Matches: matches, CandidateFacts: snapshot.Candidate.Facts, Score: score, Coverage: coverage, Qualifications: matching.Qualification(job.Job, snapshot.Profile, reqs, time.Now().UTC())}
		if err := a.Store.SaveMatchResult(ctx, user(r), strings.TrimSpace(in.MaskName), result); err != nil {
			return err
		}
		analyzed = append(analyzed, job.Job.ID)
		return nil
	}
	for _, job := range remaining {
		if len(requirements[job.Job.ID].Items) == 0 {
			if err := persist(job, nil); err != nil {
				matchFailure(w, err)
				return
			}
		}
	}
	// Bound output as well as input: a dense JD must not crowd out another
	// job's requirements. Commit each completed group before reserving more calls.
	for len(inputs) > 0 {
		count, size := 0, 0
		for count < len(inputs) {
			next := len(inputs[count].Requirements)
			if count > 0 && size+next > 24 {
				break
			}
			size += next
			count++
		}
		group := inputs[:count]
		comparisons, err := matching.Compare(ctx, budget, snapshot.Candidate, group)
		if err != nil {
			matchFailure(w, err)
			return
		}
		for _, job := range group {
			if err := persist(byID[job.ID], comparisons[job.ID]); err != nil {
				matchFailure(w, err)
				return
			}
		}
		inputs = inputs[count:]
	}
	write(w, map[string]any{"analyzed": analyzed, "reused": reused, "requirements_reused": requirementsReused, "calls": budget.calls}, nil)
}

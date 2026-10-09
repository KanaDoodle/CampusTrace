package transport

import (
	"context"
	"errors"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	"github.com/KanaDoodle/CampusTrace/internal/modelconfig"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/resume"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

type matchTaskRequest struct {
	Kind          string              `json:"kind,omitempty"`
	Company       string              `json:"company,omitempty"`
	ScopeKey      string              `json:"scope_key,omitempty"`
	InputKeys     map[string]string   `json:"input_keys"`
	JobIDs        []string            `json:"job_ids"`
	CandidateHash string              `json:"candidate_hash"`
	MaskName      string              `json:"mask_name"`
	Model         *modelconfig.Config `json:"model_config,omitempty"`
	RequestKey    string              `json:"request_key"`
	Version       int                 `json:"version"`
	Retry         bool                `json:"retry"`
}
type MatchTaskRunner struct {
	api       *API
	root      context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	active    map[string]context.CancelFunc
	wg        sync.WaitGroup
	slots     chan struct{}
	admission chan struct{}
}

// Credentials and the optional masking name live only in these execution
// closures. SQL retains IDs, input hashes, states and sanitized diagnostics.
func (a *API) StartMatchTasks(ctx context.Context, concurrency int) *MatchTaskRunner {
	if concurrency < 1 {
		concurrency = 2
	}
	root, cancel := context.WithCancel(ctx)
	runner := &MatchTaskRunner{api: a, root: root, cancel: cancel, active: map[string]context.CancelFunc{}, slots: make(chan struct{}, concurrency), admission: make(chan struct{}, 16)}
	a.MatchTasks = runner
	return runner
}
func (r *MatchTaskRunner) Close() { r.cancel(); r.wg.Wait() }
func (r *MatchTaskRunner) cancelRun(id string) {
	r.mu.Lock()
	cancel := r.active[id]
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func (r *MatchTaskRunner) available() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.root.Err() == nil && len(r.admission) < cap(r.admission)
}
func (a *API) taskModel(in matchTaskRequest) (resume.Completer, string, error) {
	identity, err := a.matchIdentity(matchPreviewRequest{MaskName: in.MaskName})
	if err != nil {
		return nil, "", err
	}
	model := a.ResumeModel
	if in.Model != nil {
		client, err := a.customModel(*in.Model)
		if err != nil {
			return nil, "", modelconfig.ErrInvalid
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
		return nil, "", p.ErrBackendUnavailable
	}
	return model, identity, nil
}
func (a *API) matchingTaskRoutes(on func(string, http.HandlerFunc)) {
	on("GET /api/matching/tasks", func(w http.ResponseWriter, r *http.Request) {
		runs, err := a.Store.MatchRuns(r.Context(), user(r))
		if err == nil {
			for i := range runs {
				if err = a.Store.HydrateMatchRun(r.Context(), user(r), &runs[i]); err != nil {
					break
				}
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, runs, err)
	})
	on("GET /api/matching/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, err := a.Store.MatchRuns(r.Context(), user(r))
		if err != nil {
			write(w, nil, err)
			return
		}
		v, err := a.Store.MatchRun(r.Context(), user(r), r.PathValue("id"))
		if err == nil {
			err = a.Store.HydrateMatchRun(r.Context(), user(r), &v)
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, v, err)
	})
	on("GET /api/matching/tasks/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Store.MatchRunEvents(r.Context(), user(r), r.PathValue("id"))
		w.Header().Set("Cache-Control", "no-store")
		write(w, v, err)
	})
	for _, route := range []string{"POST /api/matching/tasks", "POST /api/matching/tasks/{id}/resume"} {
		on(route, func(w http.ResponseWriter, r *http.Request) {
			if a.MatchTasks == nil || !a.MatchTasks.available() {
				codedError(w, 503, "MATCH_RUN_UNAVAILABLE")
				return
			}
			select {
			case a.MatchTasks.admission <- struct{}{}:
			default:
				codedError(w, 503, "MATCH_RUN_UNAVAILABLE")
				return
			}
			transferred := false
			defer func() {
				if !transferred {
					<-a.MatchTasks.admission
				}
			}()
			var in matchTaskRequest
			if err := decode(r, &in); err != nil || len(in.JobIDs) < 1 || len(in.JobIDs) > 100 {
				codedError(w, 400, "MATCH_CAPACITY")
				return
			}
			model, identity, err := a.taskModel(in)
			if err != nil {
				matchFailure(w, err)
				return
			}
			snapshot, err := a.Store.MatchSnapshot(r.Context(), user(r), identity, strings.TrimSpace(in.MaskName), in.JobIDs)
			if err != nil {
				matchFailure(w, err)
				return
			}
			if snapshot.CandidateHash != in.CandidateHash {
				matchFailure(w, p.ErrStaleInput)
				return
			}
			if in.Kind != "" && in.Kind != "COMPANY" {
				codedError(w, 400, "MATCH_JOB_UNAVAILABLE")
				return
			}
			if in.Kind == "COMPANY" {
				jobs := p.WholeJobs(snapshot.Jobs, in.MaskName)
				if err := matching.ValidateHolisticInput(snapshot.Candidate, jobs, true); err != nil {
					matchFailure(w, err)
					return
				}
				if strings.TrimSpace(in.Company) != snapshot.Jobs[0].Job.Company || in.ScopeKey != matching.CompanyInputKey(snapshot.Candidate, jobs, identity) {
					matchFailure(w, p.ErrStaleInput)
					return
				}
			}
			if len(in.JobIDs) > snapshot.Settings.RoundLimit {
				codedError(w, 400, "MATCH_CAPACITY")
				return
			}
			if len(in.InputKeys) != len(in.JobIDs) {
				matchFailure(w, p.ErrStaleInput)
				return
			}
			items := []p.MatchRunItem{}
			byID := map[string]p.MatchJob{}
			for _, j := range snapshot.Jobs {
				byID[j.Job.ID] = j
			}
			for _, id := range in.JobIDs {
				j := byID[id]
				if in.InputKeys[id] != j.InputKey {
					matchFailure(w, p.ErrStaleInput)
					return
				}
				if (in.Kind == "COMPANY" || j.AnalysisMode != matching.HolisticVersion || j.State != "ANALYZED") && (j.ExcludedReason != "" || j.TextBytes == 0 || j.TextBytes > matching.MaxBatchText) {
					codedError(w, 409, "MATCH_JOB_UNAVAILABLE")
					return
				}
				state := "QUEUED"
				if in.Kind != "COMPANY" && j.State == "ANALYZED" && j.AnalysisMode == matching.HolisticVersion {
					state = "REUSED"
				}
				items = append(items, p.MatchRunItem{JobID: id, InputKey: j.InputKey, State: state})
			}
			token := d.ID()
			var run p.MatchRun
			replay := false
			if id := r.PathValue("id"); id != "" {
				old, e := a.Store.MatchRun(r.Context(), user(r), id)
				if e != nil {
					matchFailure(w, e)
					return
				}
				if old.Kind != in.Kind || old.Company != in.Company || old.ScopeKey != in.ScopeKey || (old.Kind == "COMPANY" && len(old.Items) != len(items)) {
					matchFailure(w, p.ErrStaleInput)
					return
				}
				run, err = a.Store.ResumeMatchRun(r.Context(), user(r), id, token, in.Version, snapshot.CandidateHash, identity, items)
			} else {
				fingerprint := taskFingerprint(identity, snapshot.CandidateHash, items, in.Retry)
				if in.Kind == "COMPANY" {
					fingerprint = d.Hash(fingerprint + "\n" + in.Kind + "\n" + in.Company + "\n" + in.ScopeKey)
				}
				run, replay, err = a.Store.CreateMatchRun(r.Context(), user(r), in.RequestKey, token, p.MatchRun{Kind: in.Kind, Company: in.Company, ScopeKey: in.ScopeKey, CandidateHash: snapshot.CandidateHash, Model: identity, Items: items, RequestHash: fingerprint, RequestID: observability.From(r.Context()).RequestID})
			}
			if errors.Is(err, p.ErrMatchRunBusy) {
				codedError(w, 409, "MATCH_BUSY")
				return
			}
			if err != nil {
				matchFailure(w, err)
				return
			}
			if !replay {
				transferred = true
				a.MatchTasks.launch(user(r), run, p.MatchRunGuard{ID: run.ID, Token: token}, in, model)
			}
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusAccepted)
			write(w, run, nil)
		})
	}
	on("POST /api/matching/tasks/{id}/control", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Version int    `json:"version"`
			Action  string `json:"action"`
		}
		if err := decode(r, &in); err != nil {
			write(w, nil, err)
			return
		}
		v, err := a.Store.ControlMatchRun(r.Context(), user(r), r.PathValue("id"), in.Version, in.Action)
		if err == nil && in.Action == "CANCEL" && a.MatchTasks != nil {
			a.MatchTasks.cancelRun(v.ID)
		}
		write(w, v, err)
	})
}
func (r *MatchTaskRunner) launch(user string, run p.MatchRun, g p.MatchRunGuard, in matchTaskRequest, model resume.Completer) {
	ctx, cancel := context.WithCancel(r.root)
	ctx = p.WithMatchRunGuard(ctx, g)
	ctx = observability.With(ctx, observability.Fields{RequestID: run.RequestID, RunID: run.ID})
	r.mu.Lock()
	r.active[run.ID] = cancel
	r.wg.Add(1)
	r.mu.Unlock()
	go func() {
		defer func() { <-r.admission }()
		defer r.wg.Done()
		defer cancel()
		defer func() { r.mu.Lock(); delete(r.active, run.ID); r.mu.Unlock() }()
		heartbeatDone := make(chan struct{})
		go func() {
			defer close(heartbeatDone)
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := r.api.Store.RenewMatchRun(ctx, user, g); err != nil {
						cancel()
						return
					}
				}
			}
		}()
		defer func() {
			cancel()
			<-heartbeatDone
			cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer done()
			_ = r.api.Store.UpdateMatchRun(cleanup, user, g, func(v *p.MatchRun) error {
				if v.State == "RUNNING" || v.State == "PAUSING" {
					v.State = "WAITING_AUTH"
					for i := range v.Items {
						if v.Items[i].State == "RUNNING" {
							v.Items[i].State = "INTERRUPTED"
							v.Items[i].Code = "MATCH_INTERRUPTED"
						}
					}
				}
				return nil
			})
		}()
		queuedAt := time.Now()
		select {
		case r.slots <- struct{}{}:
			defer func() { <-r.slots }()
		case <-ctx.Done():
			return
		}
		if r.api.Metrics != nil {
			r.api.Metrics.Observe("matching_queue_seconds", time.Since(queuedAt).Seconds())
		}
		r.process(ctx, user, run, g, in, model)
	}()
}
func (r *MatchTaskRunner) process(ctx context.Context, user string, run p.MatchRun, g p.MatchRunGuard, in matchTaskRequest, model resume.Completer) {
	for ctx.Err() == nil {
		current, err := r.api.Store.MatchRun(ctx, user, run.ID)
		if err != nil {
			return
		}
		if current.State == "PAUSING" {
			_ = r.api.Store.UpdateMatchRun(ctx, user, g, func(v *p.MatchRun) error { v.State = "PAUSED"; return nil })
			return
		}
		if current.State != "RUNNING" {
			return
		}
		ids := []string{}
		for _, i := range current.Items {
			if i.State == "QUEUED" {
				ids = append(ids, i.JobID)
				if in.Kind != "COMPANY" && (len(ids) == matching.MaxBatch || in.Retry) {
					break
				}
			}
		}
		if len(ids) == 0 {
			_ = r.api.Store.UpdateMatchRun(ctx, user, g, func(v *p.MatchRun) error {
				v.State = "COMPLETED"
				hasErrors, hasPending := false, false
				for _, i := range v.Items {
					hasErrors = hasErrors || i.State == "FAILED"
					hasPending = hasPending || i.State == "PAUSED" || i.State == "INTERRUPTED"
				}
				if hasErrors {
					v.State = "COMPLETED_WITH_ERRORS"
				}
				if hasPending {
					v.State = "PAUSED"
				}
				return nil
			})
			return
		}
		snapshot, err := r.api.Store.MatchSnapshot(ctx, user, run.Model, in.MaskName, ids)
		if err != nil {
			r.fail(ctx, user, g, ids, err, "PREPARE", 0)
			return
		}
		// Pack within the existing 3-job/24KB budget, preserving batching benefits.
		bytes := 0
		packed := []string{}
		byID := map[string]p.MatchJob{}
		for _, j := range snapshot.Jobs {
			byID[j.Job.ID] = j
		}
		for _, id := range ids {
			j := byID[id]
			if in.Kind != "COMPANY" && len(packed) > 0 && bytes+j.TextBytes > matching.MaxBatchText {
				break
			}
			packed = append(packed, id)
			bytes += j.TextBytes
		}
		ids = packed
		err = r.api.Store.UpdateMatchRun(ctx, user, g, func(v *p.MatchRun) error {
			wanted := map[string]bool{}
			for _, id := range ids {
				wanted[id] = true
			}
			for i := range v.Items {
				if wanted[v.Items[i].JobID] {
					if v.Items[i].InputKey != byID[v.Items[i].JobID].InputKey || snapshot.CandidateHash != v.CandidateHash {
						return p.ErrStaleInput
					}
					v.Items[i].State = "RUNNING"
					v.Items[i].Stage = "PREPARE"
				}
			}
			return nil
		})
		if err != nil {
			r.fail(ctx, user, g, ids, err, "PREPARE", 0)
			return
		}
		phase := "PREPARE"
		phaseStart := time.Now()
		hook := func(next, job string, reviews int) error {
			if job == "" {
				if r.api.Metrics != nil {
					r.api.Metrics.Observe("matching_"+strings.ToLower(phase)+"_seconds", time.Since(phaseStart).Seconds())
				}
				_ = r.api.Store.AddMatchRunEvent(ctx, user, g, p.MatchRunEvent{RequestID: run.RequestID, Stage: phase, JobIDs: ids, DurationMS: time.Since(phaseStart).Milliseconds()})
				phase = next
				phaseStart = time.Now()
			}
			return r.api.Store.UpdateMatchRun(ctx, user, g, func(v *p.MatchRun) error {
				for i := range v.Items {
					for _, id := range ids {
						if v.Items[i].JobID == id && v.Items[i].State == "RUNNING" {
							v.Items[i].Stage = next
							if job == id {
								v.Items[i].State = "SUCCEEDED"
								v.Items[i].Reviews = reviews
							}
						}
					}
				}
				return nil
			})
		}
		batchCtx, done := context.WithTimeout(ctx, 175*time.Second)
		out, err := r.executeLeasedBatch(batchCtx, user, matchBatchInput{JobIDs: ids, CandidateHash: run.CandidateHash, MaskName: in.MaskName, Company: in.Company, ScopeKey: in.ScopeKey}, model, run.Model, hook)
		done()
		if r.api.Metrics != nil {
			r.api.Metrics.Observe("matching_"+strings.ToLower(phase)+"_seconds", time.Since(phaseStart).Seconds())
		}
		_ = r.api.Store.AddMatchRunEvent(ctx, user, g, p.MatchRunEvent{RequestID: run.RequestID, Stage: phase, JobIDs: ids, DurationMS: time.Since(phaseStart).Milliseconds()})
		if err != nil {
			var stage *MatchStageError
			if errors.As(err, &stage) {
				phase = stage.Stage
				err = stage.Err
			}
			if r.fail(ctx, user, g, ids, err, phase, out.Calls) {
				return
			}
			continue
		}
		err = r.api.Store.UpdateMatchRun(ctx, user, g, func(v *p.MatchRun) error {
			v.Calls += out.Calls
			v.RequirementsReused += out.RequirementsReused
			for i := range v.Items {
				for _, id := range out.Reused {
					if v.Items[i].JobID == id {
						v.Items[i].State = "REUSED"
					}
				}
			}
			return nil
		})
		if err != nil {
			return
		}
	}
}
func (r *MatchTaskRunner) fail(ctx context.Context, user string, g p.MatchRunGuard, ids []string, err error, stage string, calls int) bool {
	code, diagnostic := matchTaskFailure(err, stage)
	if job, ok := diagnostic["job_index"].(int); ok && job > 0 && job <= len(ids) {
		diagnostic["source_job_id"] = ids[job-1]
	}
	stop := errors.Is(err, p.ErrStaleInput) || errors.Is(err, p.ErrMatchQuota) || errors.Is(err, p.ErrMatchRunLease) || errors.Is(err, context.Canceled) || errors.Is(err, p.ErrMatchRunBusy) || errors.Is(err, p.ErrBackendUnavailable) || code == "MODEL_PROVIDER_FAILED" || code == "MODEL_RESPONSE_INVALID" || code == "MODEL_AUTH_FAILED" || code == "MODEL_BALANCE_LOW" || code == "MODEL_PROVIDER_BUSY" || code == "MODEL_CONNECTION_FAILED" || code == "MODEL_TIMEOUT"
	_ = r.api.Store.AddMatchRunEvent(ctx, user, g, p.MatchRunEvent{RequestID: observability.From(ctx).RequestID, Stage: stage, JobIDs: ids, Code: code})
	persistErr := r.api.Store.UpdateMatchRun(ctx, user, g, func(v *p.MatchRun) error {
		v.Calls += calls
		for i := range v.Items {
			for _, id := range ids {
				if v.Items[i].JobID == id && v.Items[i].State != "SUCCEEDED" && v.Items[i].State != "REUSED" {
					v.Items[i].State = "FAILED"
					v.Items[i].Stage = stage
					v.Items[i].Code = code
					v.Items[i].Diagnostic = diagnostic
				}
			}
		}
		if stop {
			v.State = "WAITING_AUTH"
		}
		return nil
	})
	if r.api.Metrics != nil {
		r.api.Metrics.Add("matching_failures", 1)
	}
	return stop || persistErr != nil
}
func matchTaskFailure(err error, stage string) (string, map[string]any) {
	code := resumeDraftFailure(err)
	switch {
	case errors.Is(err, p.ErrMatchRunBusy):
		code = "MATCH_BUSY"
	case errors.Is(err, p.ErrStaleInput):
		code = "MATCH_INPUT_CHANGED"
	case errors.Is(err, p.ErrMatchQuota):
		code = "MATCH_DAILY_LIMIT"
	case errors.Is(err, p.ErrMatchRunLease):
		code = "MATCH_INTERRUPTED"
	case errors.Is(err, matching.ErrCapacity):
		code = "MATCH_CAPACITY"
	case errors.Is(err, p.ErrNotFound):
		code = "MATCH_JOB_UNAVAILABLE"
	case errors.Is(err, p.ErrMatchJobUnavailable):
		code = "MATCH_JOB_UNAVAILABLE"
	case errors.Is(err, matching.ErrInvalid):
		code = "MATCH_OUTPUT_INVALID"
	case errors.Is(err, p.ErrBackendUnavailable):
		code = "MATCH_RUN_UNAVAILABLE"
	}
	diag := map[string]any{"stage": stage}
	addCapacityDiagnostic(diag, err)
	var v *matching.ValidationError
	if errors.As(err, &v) {
		diag["validation_reason"] = v.Reason
		addWholeDiagnostic(diag, v)
		diag["job_index"] = v.JobIndex
		diag["item_index"] = v.ItemIndex
		if v.Reason == "MATCH_COUNT" || v.Reason == "JOB_COUNT" {
			diag["expected"] = v.Expected
			diag["actual"] = v.Actual
		}
	}
	var provider *analysis.HTTPError
	if errors.As(err, &provider) {
		diag["provider_status"] = provider.Status
	}
	var response *analysis.ResponseError
	if errors.As(err, &response) {
		diag["response_reason"] = response.Reason
	}
	return code, diag
}

func taskFingerprint(model, hash string, items []p.MatchRunItem, retry bool) string {
	keys := []string{model, hash}
	if retry {
		keys = append(keys, "single")
	} else {
		keys = append(keys, "batch")
	}
	for _, i := range items {
		keys = append(keys, i.JobID, i.InputKey)
	}
	return d.Hash(d.JSON(keys))
}

// The compatibility HTTP endpoint and durable tasks share the same per-user
// external-call lease, including when two API instances receive requests.
func (r *MatchTaskRunner) executeLeasedBatch(ctx context.Context, user string, in matchBatchInput, model resume.Completer, identity string, hook func(string, string, int) error) (matchBatchOutcome, error) {
	if r.api.Queue == nil {
		return matchBatchOutcome{}, p.ErrBackendUnavailable
	}
	key := r.api.Queue.Prefix + "matching:lease:" + user
	token := d.ID()
	ok, err := r.api.Queue.R.SetNX(ctx, key, token, 4*time.Minute).Result()
	if err != nil {
		return matchBatchOutcome{}, p.ErrBackendUnavailable
	}
	if !ok {
		return matchBatchOutcome{}, p.ErrMatchRunBusy
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer done()
		r.api.Queue.R.Eval(cleanup, `if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) end return 0`, []string{key}, token)
	}()
	started := time.Now()
	var out matchBatchOutcome
	if in.Company != "" {
		out, err = r.api.executeCompanyBatch(ctx, user, in, model, identity, hook)
	} else {
		out, err = r.api.executeMatchBatch(ctx, user, in, model, identity, hook)
	}
	fields := observability.From(ctx)
	code := ""
	var diagnostic map[string]any
	if err != nil {
		stage := ""
		var phase *MatchStageError
		if errors.As(err, &phase) {
			stage = phase.Stage
		}
		code, diagnostic = matchTaskFailure(err, stage)
	}
	slog.InfoContext(ctx, "matching batch completed", "request_id", fields.RequestID, "run_id", fields.RunID, "jobs", len(in.JobIDs), "duration_ms", time.Since(started).Milliseconds(), "model_calls", out.Calls, "error_code", code, "diagnostic", diagnostic)
	return out, err
}

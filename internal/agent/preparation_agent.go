package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/projectdefense"
)

type PreparationEnvironment struct {
	Model         Model
	ModelIdentity string
	ModelLabel    string
	EvidenceKind  string
	MaskName      string
	Tools         *Tools
	Budget        int
}
type PreparationReview struct {
	Defense        *d.DefenseState       `json:"defense,omitempty"`
	Input          d.PreparationInput    `json:"input"`
	Sources        []d.PreparationSource `json:"sources"`
	Missing        []string              `json:"missing"`
	Model          string                `json:"model"`
	EvidenceKind   string                `json:"evidence_kind"`
	Budget         int                   `json:"budget"`
	SourceIdentity string                `json:"source_identity"`
	ReviewHash     string                `json:"review_hash"`
	PreviousPlan   *d.PreparationPlan    `json:"previous_plan,omitempty"`
	MemoryRevision uint64                `json:"-"`
}
type PreparationRequest struct {
	Input        d.PreparationInput `json:"input"`
	RequestKey   string             `json:"request_key"`
	ReviewHash   string             `json:"review_hash"`
	Confirm      bool               `json:"confirm"`
	RetryUnknown bool               `json:"retry_unknown"`
}
type PreparationRunner struct {
	Graph  *PreparationGraphClient
	Store  *p.Store
	Allow  func(context.Context, string) (bool, error)
	root   context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	active map[string]context.CancelFunc
	slots  chan struct{}
	wg     sync.WaitGroup
	closed bool
}

func NewPreparationRunner(ctx context.Context, store *p.Store) *PreparationRunner {
	root, cancel := context.WithCancel(ctx)
	return &PreparationRunner{Graph: PreparationGraphFromEnvironment(), Store: store, root: root, cancel: cancel, active: map[string]context.CancelFunc{}, slots: make(chan struct{}, 4)}
}

// Explicit compatibility constructor for historical regression; never selected on failure.
func NewLegacyPreparationRunner(ctx context.Context, store *p.Store) *PreparationRunner {
	r := NewPreparationRunner(ctx, store)
	r.Graph = nil
	return r
}
func (r *PreparationRunner) Close() {
	r.mu.Lock()
	r.closed = true
	r.cancel()
	r.mu.Unlock()
	r.wg.Wait()
}
func (r *PreparationRunner) Cancel(id string) {
	r.mu.Lock()
	cancel := r.active[id]
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func (r *PreparationRunner) Preview(ctx context.Context, user string, in d.PreparationInput, env PreparationEnvironment) (PreparationReview, error) {
	out := PreparationReview{}
	in = NormalizePreparationInput(in)
	if ValidatePreparationInput(in) != nil || len(env.MaskName) > 200 || env.Budget < 0 || env.Budget > 200000 {
		return out, p.ErrValidation
	}
	tools := Tools{MaskName: env.MaskName}
	in.Goal = tools.memoryText(in.Goal)
	in.Feedback = tools.memoryText(in.Feedback)
	if in.Defense != nil {
		copyInput := *in.Defense
		copyInput.Answer = tools.memoryText(copyInput.Answer)
		in.Defense = &copyInput
	}
	identity, rev, err := r.Store.PreparationAgentIdentity(ctx, user, in.ApplicationID, in.OtherReviews)
	if err != nil {
		return out, err
	}
	sources, missing, err := r.materials(ctx, user, in, env.MaskName)
	if err != nil {
		return out, err
	}
	var previous *d.PreparationPlan
	var previousDefense *d.DefenseState
	if in.ParentID != "" {
		parent, err := r.Store.AgentExecution(ctx, user, in.ParentID)
		if err != nil {
			return out, err
		}
		if parent.Preparation == nil || parent.Preparation.Input.ApplicationID != in.ApplicationID || parent.SourceIdentity != identity || parent.Preparation.ModelIdentity != d.Hash(env.ModelIdentity+env.MaskName) || (parent.State != "COMPLETED" && parent.Preparation.Question == "") {
			return out, p.ErrStaleInput
		}
		previous = parent.Preparation.Plan
		if (in.Defense != nil) != (parent.Preparation.Defense != nil) {
			return out, p.ErrValidation
		}
		if in.Defense != nil {
			previousDefense = parent.Preparation.Defense
			previous = nil
		}
		if previous == nil && len(in.Mastered)+len(in.Completed) > 0 {
			return out, p.ErrValidation
		}
		allowed := map[string]bool{}
		if previous != nil {
			for _, item := range previous.Items {
				allowed[item.ID] = true
			}
		}
		seen := map[string]bool{}
		for _, id := range append(append([]string{}, in.Mastered...), in.Completed...) {
			if !allowed[id] || seen[id] {
				return out, p.ErrValidation
			}
			seen[id] = true
		}
		in.Mastered = append(in.Mastered, parent.Preparation.Input.Mastered...)
		in.Completed = append(in.Completed, parent.Preparation.Input.Completed...)
		// Reuse only completed reads with the same source identity. Pending tools and
		// unknown model requests from a predecessor never become completed evidence.
		sources = append([]d.PreparationSource{}, parent.Preparation.Sources...)
	}
	defense, err := newDefenseState(in, previousDefense)
	if err != nil {
		return out, err
	}
	if defense != nil && in.Defense.MemoryMode != "off" {
		rows, e := r.Store.DefenseMemories(ctx, user, defense.Topic)
		if e != nil {
			return out, e
		}
		defense.Memories, defense.MemoryExcluded = p.SelectDefenseMemories(rows, defense.Topic, defense.Version, time.Now().UTC())
		// Apply this call's extra masking to recalled user text as well.
		if env.MaskName != "" {
			var masked []d.DefenseMemory
			if e = json.Unmarshal([]byte(tools.memoryText(d.JSON(defense.Memories))), &masked); e != nil {
				return out, p.ErrValidation
			}
			defense.Memories = masked
		}
	}
	after, _, err := r.Store.PreparationAgentIdentity(ctx, user, in.ApplicationID, in.OtherReviews)
	if err != nil {
		return out, err
	}
	if after != identity {
		return out, p.ErrStaleInput
	}
	budget := env.Budget
	if budget == 0 {
		budget = 128000
	}
	if budget < 4096 {
		return out, p.ErrValidation
	}
	out = PreparationReview{Defense: defense, Input: in, Sources: sources, Missing: missing, Model: env.ModelLabel, EvidenceKind: env.EvidenceKind, Budget: budget, SourceIdentity: identity, MemoryRevision: rev, PreviousPlan: previous}
	version := PreparationAgentVersion
	if r.Graph != nil {
		version += "+" + PreparationGraphVersion
	}
	out.ReviewHash = d.Hash(version + env.ModelIdentity + env.MaskName + d.JSON(out))
	return out, nil
}
func (r *PreparationRunner) Start(ctx context.Context, user string, req PreparationRequest, env PreparationEnvironment) (p.AgentExecution, error) {
	if req.Input.Defense != nil && r.Graph == nil {
		return p.AgentExecution{}, p.ErrBackendUnavailable
	}
	if !req.Confirm || !pExecutionKey(req.RequestKey) || env.Model == nil {
		return p.AgentExecution{}, p.ErrValidation
	}
	if r.Graph != nil {
		if err := r.Graph.Health(ctx); err != nil {
			return p.AgentExecution{}, err
		}
	}
	review, err := r.Preview(ctx, user, req.Input, env)
	if err != nil {
		return p.AgentExecution{}, err
	}
	if req.ReviewHash != review.ReviewHash {
		return p.AgentExecution{}, p.ErrStaleInput
	}
	messages := preparationMessages(review.Input, review.Sources, review.Missing, review.PreviousPlan)
	state := &d.PreparationState{Input: review.Input, ModelIdentity: d.Hash(env.ModelIdentity + env.MaskName), ModelLabel: review.Model, EvidenceKind: review.EvidenceKind, ReviewHash: review.ReviewHash, Revision: 1, ProviderState: "READY", AttemptKeys: []string{}, Messages: json.RawMessage(d.JSON(messages)), Sources: review.Sources, Missing: review.Missing, PreviousPlan: review.PreviousPlan, Usage: json.RawMessage(d.JSON(TokenBudget{Limit: review.Budget})), Trace: []d.PreparationTrace{{Kind: "context", Status: "FROZEN", Detail: "已绑定本申请留档与授权资料范围"}}}
	state.Defense = review.Defense
	if state.Defense != nil {
		messages = defenseMessages(review.Input, review.Sources, review.Missing, state.Defense)
		state.Messages = json.RawMessage(d.JSON(messages))
	}
	if r.Graph != nil {
		state.QualityVersion = PreparationQualityVersion
		messages[0].Content += preparationQualityContext(state)
		state.Messages = json.RawMessage(d.JSON(messages))
		state.Engine = "langgraph"
		state.GraphVersion = PreparationGraphVersion
	}
	if review.Input.ParentID != "" {
		parent, err := r.Store.AgentExecution(ctx, user, review.Input.ParentID)
		if err != nil {
			return p.AgentExecution{}, err
		}
		state.Revision = parent.Preparation.Revision + 1
	}
	version := PreparationAgentVersion
	if r.Graph != nil {
		version += "+" + PreparationGraphVersion
	}
	input := json.RawMessage(d.JSON(review.Input))
	v, created, err := r.Store.BeginAgentExecutionOnce(ctx, user, p.AgentExecution{SkillID: d.PreparationAgentSkill, SkillVersion: "1", HarnessVersion: version, RequestKey: req.RequestKey, Fingerprint: d.Hash(string(input) + review.ReviewHash), Input: input, Identity: review.ReviewHash, SourceIdentity: review.SourceIdentity, MemoryRevision: review.MemoryRevision, Preparation: state})
	if err != nil {
		return v, err
	}
	if !created {
		return r.Store.AgentExecution(ctx, user, v.ID)
	}
	return r.Resume(ctx, user, v.ID, req, env)
}
func pExecutionKey(key string) bool {
	if len(key) < 8 || len(key) > 64 {
		return false
	}
	for _, c := range key {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func (r *PreparationRunner) Resume(ctx context.Context, user, id string, req PreparationRequest, env PreparationEnvironment) (p.AgentExecution, error) {
	if !req.Confirm || !pExecutionKey(req.RequestKey) || env.Model == nil {
		return p.AgentExecution{}, p.ErrValidation
	}
	v, err := r.Store.AgentExecution(ctx, user, id)
	if err != nil {
		return v, err
	}
	if v.Preparation == nil || v.SkillID != d.PreparationAgentSkill {
		return v, p.ErrValidation
	}
	state := v.Preparation
	if state.Defense != nil && state.Defense.Version != projectdefense.Version() {
		return v, p.ErrStaleInput
	}
	if req.ReviewHash != state.ReviewHash || state.ModelIdentity != d.Hash(env.ModelIdentity+env.MaskName) {
		return v, p.ErrStaleInput
	}
	if v.State == "COMPLETED" || v.State == "CANCELLED" || v.State == "STALE" || v.State == "FAILED" {
		return v, nil
	}
	if state.Question != "" {
		return v, p.ErrConflict
	}
	if (state.Engine == "langgraph") != (r.Graph != nil) || (r.Graph != nil && state.GraphVersion != PreparationGraphVersion) {
		return v, p.ErrConflict
	}
	if r.Graph != nil {
		if err := r.Graph.Health(ctx); err != nil {
			return v, err
		}
	}
	current, _, err := r.Store.PreparationAgentIdentity(ctx, user, state.Input.ApplicationID, state.Input.OtherReviews)
	if err != nil {
		return v, err
	}
	if current != v.SourceIdentity {
		return v, p.ErrStaleInput
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return v, p.ErrBackendUnavailable
	}
	select {
	case r.slots <- struct{}{}:
		r.wg.Add(1)
	default:
		r.mu.Unlock()
		return v, p.ErrQueueBusy
	}
	r.mu.Unlock()
	transferred := false
	defer func() {
		if !transferred {
			<-r.slots
			r.wg.Done()
		}
	}()
	if r.Allow != nil {
		allowed, e := r.Allow(ctx, user)
		if e != nil {
			return v, e
		}
		if !allowed {
			return v, p.ErrQueueBusy
		}
	}
	v, lease, err := r.Store.ClaimAgentPreparation(ctx, user, id, p.NarrationClaim{AttemptKey: req.RequestKey, RetryUnknown: req.RetryUnknown})
	if err != nil {
		return v, err
	}
	if lease == "" {
		return r.Store.AgentExecution(ctx, user, id)
	}
	transferred = true
	go func() { defer func() { <-r.slots; r.wg.Done() }(); r.run(user, lease, v, env) }()
	return r.Store.AgentExecution(ctx, user, id)
}
func (r *PreparationRunner) run(user, lease string, v p.AgentExecution, env PreparationEnvironment) {
	ctx, cancel := context.WithTimeout(r.root, 35*time.Second)
	defer cancel()
	r.mu.Lock()
	r.active[v.ID] = cancel
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.active, v.ID); r.mu.Unlock() }()
	watch, stop := context.WithCancel(ctx)
	defer stop()
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-watch.Done():
				return
			case <-ticker.C:
				current, err := r.Store.AgentExecution(watch, user, v.ID)
				if err == nil && (current.State == "CANCELLED" || current.State == "STALE" || current.State == "FAILED") {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { stop(); <-watched }()
	checkpoint := func() error {
		if v.Preparation.Defense != nil && v.Preparation.Defense.Version != projectdefense.Version() {
			v.State = "STALE"
			return p.ErrStaleInput
		}
		identity, _, err := r.Store.PreparationAgentIdentity(ctx, user, v.Preparation.Input.ApplicationID, v.Preparation.Input.OtherReviews)
		if err != nil {
			return err
		}
		if identity != v.SourceIdentity {
			v.State = "STALE"
			v.Answer = "资料已变化，请用当前授权材料重新开始。"
			return p.ErrStaleInput
		}
		return r.Store.CheckpointAgentExecution(ctx, user, lease, v)
	}
	var err error
	if r.Graph != nil {
		err = r.Graph.Run(ctx, user, &v, env.Model, preparationToolset{tools: env.Tools, input: v.Preparation.Input}, checkpoint)
	} else {
		err = RunPreparationLoop(ctx, user, &v, env.Model, preparationToolset{tools: env.Tools, input: v.Preparation.Input}, checkpoint)
	}
	if v.State == "RUNNING" {
		v.State = "PAUSED"
		v.Answer = "准备服务连接中断，已完成步骤已保存；请检查服务后继续。"
	}
	if v.Preparation.ProviderState == "DISPATCHED" {
		v.Preparation.ProviderState = "UNKNOWN"
	}
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, p.ErrStaleInput) && v.Preparation.ProviderState != "UNKNOWN" {
		v.Answer = "本轮执行未完成，可查看已保存轨迹后继续。"
	}
	cleanup, done := context.WithTimeout(context.WithoutCancel(r.root), 2*time.Second)
	defer done()
	_ = r.Store.CheckpointAgentExecution(cleanup, user, lease, v)
}
func (r *PreparationRunner) SavePreview(ctx context.Context, user, id string) (p.SavePreparationArgs, error) {
	v, err := r.Store.AgentExecution(ctx, user, id)
	if err != nil {
		return p.SavePreparationArgs{}, err
	}
	if v.Preparation == nil || v.State != "COMPLETED" || v.Preparation.Plan == nil {
		return p.SavePreparationArgs{}, p.ErrConflict
	}
	if v.Preparation.Defense != nil && v.Preparation.Defense.Version != projectdefense.Version() {
		return p.SavePreparationArgs{}, p.ErrStaleInput
	}
	if v.Preparation.Defense != nil && len(v.Preparation.Plan.Items) == 0 {
		return p.SavePreparationArgs{}, p.ErrConflict
	}
	identity, _, err := r.Store.PreparationAgentIdentity(ctx, user, v.Preparation.Input.ApplicationID, v.Preparation.Input.OtherReviews)
	if err != nil {
		return p.SavePreparationArgs{}, err
	}
	if identity != v.SourceIdentity {
		return p.SavePreparationArgs{}, p.ErrStaleInput
	}
	dossier, err := r.Store.ApplicationDossier(ctx, user, v.Preparation.Input.ApplicationID)
	if err != nil {
		return p.SavePreparationArgs{}, err
	}
	args := p.SavePreparationArgs{ExecutionID: id, Revision: v.Preparation.Revision, PlanHash: d.Hash(d.JSON(v.Preparation.Plan)), ApplicationVersion: dossier.Application.Version}
	if v.Preparation.Defense != nil {
		args.EvidenceVersion = v.Preparation.Defense.Version
	}
	return args, nil
}

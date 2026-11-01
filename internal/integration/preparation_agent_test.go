package integration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/rag"
)

type prepControlledModel struct {
	calls   atomic.Int32
	failAt  int32
	wait    bool
	entered chan struct{}
	stopped chan struct{}
}

func (m *prepControlledModel) Next(ctx context.Context, messages []agent.Message, defs []agent.Definition) (agent.Reply, error) {
	call := m.calls.Add(1)
	if m.entered != nil && call == 1 {
		close(m.entered)
	}
	if m.wait {
		<-ctx.Done()
		if m.stopped != nil {
			close(m.stopped)
		}
		return agent.Reply{}, ctx.Err()
	}
	if call == m.failAt {
		return agent.Reply{}, errors.New("controlled interruption")
	}
	var data struct {
		Goal     d.PreparationInput    `json:"goal"`
		Sources  []d.PreparationSource `json:"sources"`
		Previous *d.PreparationPlan    `json:"previous_plan"`
	}
	if len(messages) < 2 || json.Unmarshal([]byte(messages[1].Content), &data) != nil {
		return agent.Reply{}, errors.New("missing fixture context")
	}
	if data.Goal.Minutes == 0 {
		return agent.Reply{Calls: []agent.Call{{ID: d.ID(), Name: "ask_preparation_question", Args: json.RawMessage(`{"question":"你还可以准备多少分钟？"}`)}}, Usage: analysis.Usage{Known: true, PromptTokens: 100, CompletionTokens: 30}}, nil
	}
	if len(messages) == 2 && data.Previous == nil {
		return agent.Reply{Calls: []agent.Call{{ID: d.ID(), Name: "search_preparation_knowledge", Args: json.RawMessage(`{"query":"Go 并发 MySQL 索引"}`)}}, Usage: analysis.Usage{Known: true, PromptTokens: 100, CompletionTokens: 30}}, nil
	}
	plan := d.PreparationPlan{Summary: "按照剩余时间准备，保留留档依据", Items: []d.PreparationItem{}}
	if data.Previous != nil {
		excluded := map[string]bool{}
		for _, id := range append(append([]string{}, data.Goal.Mastered...), data.Goal.Completed...) {
			excluded[id] = true
		}
		for _, item := range data.Previous.Items {
			if !excluded[item.ID] {
				plan.Items = append(plan.Items, item)
			}
		}
	} else {
		var source d.PreparationSource
		for _, s := range data.Sources {
			if s.Kind == "JD" {
				source = s
				break
			}
		}
		for i, title := range []string{"Go 并发与取消", "MySQL 查询与索引", "项目实现与取舍"} {
			refs := []d.PreparationReference{}
			basis := "GENERAL"
			if source.ID != "" && i < 2 {
				basis = "JD"
				quote := strings.Split(source.Text, "\n")[0]
				refs = append(refs, d.PreparationReference{SourceID: source.ID, Quote: quote})
			}
			plan.Items = append(plan.Items, d.PreparationItem{ID: []string{"go", "mysql", "project"}[i], Title: title, Minutes: 60, Day: 1, Basis: basis, Action: "按材料说明实现、约束与故障处理，并演练回答", Deliverable: "一份可检查的回答提纲", References: refs})
		}
	}
	if len(plan.Items) > 0 {
		for i := range plan.Items {
			plan.Items[i].Minutes = data.Goal.Minutes / len(plan.Items)
		}
	}
	return agent.Reply{Calls: []agent.Call{{ID: d.ID(), Name: "finish_preparation_plan", Args: json.RawMessage(d.JSON(plan))}}, Usage: analysis.Usage{Known: true, PromptTokens: 200, CompletionTokens: 80}}, nil
}
func preparationFixture(t *testing.T) (context.Context, *p.Store, *agent.Tools, string, string) {
	t.Helper()
	ctx, s, q, user, source := setup(t)
	observation, err := s.Ingest(ctx, ingest(t, ctx, s, source))
	must(t, err)
	raw, err := s.ApplyAction(ctx, user, d.ID(), "record_submitted_application", []byte(d.JSON(p.CreateArgs{JobID: observation.JobID, Resume: "synthetic-frozen", ResumeText: "Synthetic frozen résumé: implemented Go worker cancellation."})))
	must(t, err)
	var application d.Application
	must(t, json.Unmarshal(raw, &application))
	return ctx, s, &agent.Tools{Store: s, Queue: q, RAG: &rag.Service{Store: s, Sem: make(chan struct{}, 1)}}, user, application.ID
}
func awaitPreparation(t *testing.T, ctx context.Context, s *p.Store, user, id string) p.AgentExecution {
	t.Helper()
	until := time.Now().Add(6 * time.Second)
	for time.Now().Before(until) {
		v, err := s.AgentExecution(ctx, user, id)
		must(t, err)
		if v.State != "RUNNING" && v.State != "PENDING" {
			return v
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("preparation did not finish")
	return p.AgentExecution{}
}
func startPreparation(t *testing.T, ctx context.Context, r *agent.PreparationRunner, user string, in d.PreparationInput, env agent.PreparationEnvironment) (p.AgentExecution, agent.PreparationRequest) {
	t.Helper()
	review, err := r.Preview(ctx, user, in, env)
	must(t, err)
	req := agent.PreparationRequest{Input: in, RequestKey: d.ID(), ReviewHash: review.ReviewHash, Confirm: true}
	v, err := r.Start(ctx, user, req, env)
	must(t, err)
	return v, req
}
func TestPreparationAgentPlanFeedbackAndConfirmedSave(t *testing.T) {
	ctx, s, tools, user, app := preparationFixture(t)
	model := &prepControlledModel{}
	env := agent.PreparationEnvironment{Model: model, ModelIdentity: "controlled-model-v1", ModelLabel: "controlled-preparation", EvidenceKind: "controlled-model-response", Tools: tools}
	runner := agent.NewLegacyPreparationRunner(ctx, s)
	defer runner.Close()
	input := d.PreparationInput{ApplicationID: app, Goal: "安排面试准备", Minutes: 180, Days: 3, OtherReviews: true}
	review, err := runner.Preview(ctx, user, input, env)
	must(t, err)
	if model.calls.Load() != 0 || len(review.Sources) == 0 {
		t.Fatal("preview called model or omitted frozen source")
	}
	other, err := s.NewUser(ctx, d.ID()+"@synthetic.test", "unused")
	must(t, err)
	if _, err := runner.Preview(ctx, other, input, env); !errors.Is(err, p.ErrNotFound) {
		t.Fatal("foreign application exposed", err)
	}
	v, request := startPreparation(t, ctx, runner, user, input, env)
	v = awaitPreparation(t, ctx, s, user, v.ID)
	if v.State != "COMPLETED" || v.Preparation.Plan == nil || model.calls.Load() != 2 {
		t.Fatal(v, model.calls.Load())
	}
	repeated, err := runner.Start(ctx, user, request, env)
	must(t, err)
	if repeated.ID != v.ID || model.calls.Load() != 2 {
		t.Fatal("start replay called model")
	}
	if v.Preparation.SavedAt != nil {
		t.Fatal("model saved business plan")
	}
	args, err := runner.SavePreview(ctx, user, v.ID)
	must(t, err)
	pending, err := tools.Propose(ctx, user, "save_preparation_plan", []byte(d.JSON(args)))
	must(t, err)
	before, err := s.AgentExecution(ctx, user, v.ID)
	must(t, err)
	if before.Preparation.SavedAt != nil {
		t.Fatal("proposal performed a write")
	}
	receipt, err := tools.Confirm(ctx, user, pending.ID)
	must(t, err)
	replay, err := tools.Confirm(ctx, user, pending.ID)
	must(t, err)
	if string(receipt) != string(replay) {
		t.Fatal("confirmation replay changed receipt")
	}
	saved, err := s.AgentExecution(ctx, user, v.ID)
	must(t, err)
	if saved.Preparation.SavedAt == nil {
		t.Fatal("confirmation did not save")
	}
	var receipts int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM action_receipts WHERE id=?", pending.ID).Scan(&receipts))
	if receipts != 1 {
		t.Fatal(receipts)
	}
	next := input
	next.ParentID = v.ID
	next.Mastered = []string{"go"}
	next.Feedback = "Go 并发已经掌握，只剩一小时" // normalizes 180 -> 60
	child, _ := startPreparation(t, ctx, runner, user, next, env)
	child = awaitPreparation(t, ctx, s, user, child.ID)
	if child.State != "COMPLETED" || child.Preparation.Input.Minutes != 60 || child.Preparation.Revision != 2 {
		t.Fatal(child)
	}
	total := 0
	for _, item := range child.Preparation.Plan.Items {
		if item.ID == "go" {
			t.Fatal("mastered item rescheduled")
		}
		total += item.Minutes
	}
	if total > 60 || child.Preparation.SavedAt != nil {
		t.Fatal("feedback constraints or independent save broken", child)
	}
	// Mutation after proposal must reject, even when a caller replays an old body.
	newArgs, err := runner.SavePreview(ctx, user, child.ID)
	must(t, err)
	stale, err := tools.Propose(ctx, user, "save_preparation_plan", []byte(d.JSON(newArgs)))
	must(t, err)
	_, _, err = tools.RAG.ImportDocument(ctx, user, rag.Document{Title: "Changed source", Text: "New interview material"})
	must(t, err)
	if _, err := tools.Confirm(ctx, user, stale.ID); !errors.Is(err, p.ErrStaleInput) {
		t.Fatal("stale plan saved", err)
	}
}
func TestPreparationAgentUnknownCancelAndClarification(t *testing.T) {
	t.Run("unknown_requires_new_explicit_attempt", func(t *testing.T) {
		ctx, s, tools, user, app := preparationFixture(t)
		model := &prepControlledModel{failAt: 2}
		env := agent.PreparationEnvironment{Model: model, ModelIdentity: "fixture", Tools: tools, EvidenceKind: "controlled-model-response"}
		runner := agent.NewLegacyPreparationRunner(ctx, s)
		defer runner.Close()
		in := d.PreparationInput{ApplicationID: app, Goal: "面试准备", Minutes: 60, Days: 1}
		v, req := startPreparation(t, ctx, runner, user, in, env)
		v = awaitPreparation(t, ctx, s, user, v.ID)
		if v.Preparation.ProviderState != "UNKNOWN" || v.Next != 1 || len(v.Observations) != 1 {
			t.Fatal("completed read checkpoint lost", v)
		}
		req.RequestKey = d.ID()
		if _, err := runner.Resume(ctx, user, v.ID, req, env); !errors.Is(err, p.ErrConflict) {
			t.Fatal("unknown replay without confirmation", err)
		}
		if model.calls.Load() != 2 {
			t.Fatal("unknown call auto-retried")
		}
		req.RetryUnknown = true
		_, err := runner.Resume(ctx, user, v.ID, req, env)
		must(t, err)
		v = awaitPreparation(t, ctx, s, user, v.ID)
		if v.State != "COMPLETED" || model.calls.Load() != 3 || len(v.Observations) != 1 {
			t.Fatal("resume repeated completed read", v, model.calls.Load())
		}
	})
	t.Run("cancel_fences_late_model", func(t *testing.T) {
		ctx, s, tools, user, app := preparationFixture(t)
		model := &prepControlledModel{wait: true, entered: make(chan struct{}), stopped: make(chan struct{})}
		env := agent.PreparationEnvironment{Model: model, ModelIdentity: "fixture", Tools: tools}
		runner := agent.NewLegacyPreparationRunner(ctx, s)
		defer runner.Close()
		v, _ := startPreparation(t, ctx, runner, user, d.PreparationInput{ApplicationID: app, Goal: "面试准备", Minutes: 60, Days: 1}, env)
		select {
		case <-model.entered:
		case <-time.After(2 * time.Second):
			t.Fatal("not dispatched")
		}
		must(t, s.CancelAgentExecution(ctx, user, v.ID))
		runner.Cancel(v.ID)
		select {
		case <-model.stopped:
		case <-time.After(time.Second):
			t.Fatal("model not canceled")
		}
		current, err := s.AgentExecution(ctx, user, v.ID)
		must(t, err)
		if current.State != "CANCELLED" || current.Preparation.Plan != nil {
			t.Fatal("late completion rewrote cancellation", current)
		}
	})
	t.Run("missing_time_is_clarified", func(t *testing.T) {
		ctx, s, tools, user, app := preparationFixture(t)
		model := &prepControlledModel{}
		env := agent.PreparationEnvironment{Model: model, ModelIdentity: "fixture", Tools: tools}
		runner := agent.NewLegacyPreparationRunner(ctx, s)
		defer runner.Close()
		in := d.PreparationInput{ApplicationID: app, Goal: "面试准备", Minutes: 0, Days: 3}
		v, _ := startPreparation(t, ctx, runner, user, in, env)
		v = awaitPreparation(t, ctx, s, user, v.ID)
		if v.Preparation.Question == "" || v.State != "PAUSED" {
			t.Fatal(v)
		}
		in.ParentID = v.ID
		in.Minutes = 60
		in.Feedback = "还剩一小时"
		child, _ := startPreparation(t, ctx, runner, user, in, env)
		child = awaitPreparation(t, ctx, s, user, child.ID)
		if child.State != "COMPLETED" || child.Preparation.Input.Minutes != 60 {
			t.Fatal(child)
		}
	})
}

func TestPreparationAgentFrozenProfileAndOwnerSaveFence(t *testing.T) {
	ctx, s, tools, user, app := preparationFixture(t)
	model := &prepControlledModel{}
	env := agent.PreparationEnvironment{Model: model, ModelIdentity: "fixture", Tools: tools}
	runner := agent.NewLegacyPreparationRunner(ctx, s)
	defer runner.Close()
	in := d.PreparationInput{ApplicationID: app, Goal: "根据本次留档准备", Minutes: 60, Days: 1}
	before, err := runner.Preview(ctx, user, in, env)
	must(t, err)
	must(t, s.SaveProfile(ctx, user, d.Profile{GraduationYear: 2027, Degree: "MASTER", PreferredTypes: []string{"FULL_TIME"}, Skills: []string{"rust-new-current-profile"}}))
	after, err := runner.Preview(ctx, user, in, env)
	must(t, err)
	if before.SourceIdentity != after.SourceIdentity {
		t.Fatal("today profile invalidated immutable submission context")
	}
	for _, source := range after.Sources {
		if strings.Contains(source.Text, "rust-new-current-profile") {
			t.Fatal("current profile substituted for frozen materials")
		}
	}
	v, _ := startPreparation(t, ctx, runner, user, in, env)
	v = awaitPreparation(t, ctx, s, user, v.ID)
	other, err := s.NewUser(ctx, d.ID()+"@synthetic.test", "unused")
	must(t, err)
	if _, err := runner.SavePreview(ctx, other, v.ID); !errors.Is(err, p.ErrNotFound) {
		t.Fatal("foreign save preview exposed plan", err)
	}
	// A legacy row has no context. Its source set must remain historical-unknown.
	legacy := d.Application{ID: d.ID(), UserID: user, State: "APPLIED", Version: 1}

	// Existing applications are unique per user/job; use a separate owned job.
	observation, err := s.IngestForUser(ctx, user, p.Ingest{Company: "Synthetic Legacy", Title: "Legacy Go", JobType: "FULL_TIME", ExternalID: d.ID(), Text: "Go", FetchStatus: "SUCCESS"})
	must(t, err)
	legacy.JobID = observation.JobID
	_, err = s.DB.ExecContext(ctx, "INSERT INTO applications(id,user_id,job_id,version,body) VALUES(?,?,?,?,?)", legacy.ID, user, legacy.JobID, 1, d.JSON(legacy))
	must(t, err)
	in.ApplicationID = legacy.ID
	missing, err := runner.Preview(ctx, user, in, env)
	must(t, err)
	if len(missing.Sources) != 0 || len(missing.Missing) == 0 {
		t.Fatal("legacy application borrowed current materials", missing)
	}
}

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func TestPreparationRevisionExposesRemainingChoicesAndRepairsBasis(t *testing.T) {
	raw, err := os.ReadFile("testdata/preparation-tasks.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []PreparationEvalCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	var c PreparationEvalCase
	for _, task := range cases {
		if task.ID == "feedback-mastered-and-one-hour" {
			c = task
		}
	}
	state := &d.PreparationState{Input: NormalizePreparationInput(c.Input), Sources: c.Sources, PreviousPlan: c.PreviousPlan, ProviderState: "READY"}
	state.Messages = json.RawMessage(d.JSON([]Message{{Role: "system", Content: PreparationPolicy}, {Role: "user", Content: "只剩一小时，Go 已掌握"}}))
	v := p.AgentExecution{State: "RUNNING", Preparation: state}
	calls := 0
	model := prepModelFunc(func(_ context.Context, messages []Message, defs []Definition) (Reply, error) {
		calls++
		items := defs[2].Parameters["properties"].(map[string]any)["items"].(map[string]any)
		fields := items["items"].(map[string]any)["properties"].(map[string]any)
		ids := fields["id"].(map[string]any)["enum"].([]string)
		if len(ids) != 1 || ids[0] != "mysql" || items["maxItems"] != 1 || fields["minutes"].(map[string]any)["maximum"] != 60 {
			t.Fatal("remaining choices not exposed", items)
		}
		item := c.PreviousPlan.Items[1]
		item.References = []d.PreparationReference{{SourceID: c.Sources[0].ID, Quote: "MySQL 索引与事务"}}
		if calls == 1 {
			item.Basis = "HISTORY"
		} else if !strings.Contains(messages[len(messages)-1].Content, "basis") {
			t.Fatal("precise repair feedback missing", messages)
		}
		plan := d.PreparationPlan{Summary: "保留 MySQL 准备", Items: []d.PreparationItem{item}}
		return Reply{Calls: []Call{{ID: "repair-" + string(rune('0'+calls)), Name: "finish_preparation_plan", Args: json.RawMessage(d.JSON(plan))}}}, nil
	})
	if err := RunPreparationLoop(context.Background(), "owner", &v, model, nil, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if v.State != "COMPLETED" || calls != 2 || state.Plan.Items[0].ID != "mysql" || state.Plan.Items[0].Basis != "JD" {
		t.Fatal(v.State, state.Plan, calls)
	}
	if _, exists := preparationDefinitions()[2].Parameters["properties"].(map[string]any)["items"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["id"].(map[string]any)["enum"]; exists {
		t.Fatal("state schema mutated shared definitions")
	}
}

func TestPreparationEvaluationRetainsRejectedAndMalformedCandidates(t *testing.T) {
	s := prepTestState()
	c := PreparationEvalCase{ID: "rejected", Input: s.Input, Sources: s.Sources, Expected: "COMPLETED"}
	plan := prepTestPlan(s)
	plan.Items[0].Minutes = s.Input.Minutes + 1
	r := EvalPreparationCase(context.Background(), c, "single-prompt", prepModelFunc(func(context.Context, []Message, []Definition) (Reply, error) {
		return Reply{Calls: []Call{{ID: "rejected", Name: "finish_preparation_plan", Args: json.RawMessage(d.JSON(plan))}}}, nil
	}))
	if r.CandidatePlan == nil || r.Plan != nil || !strings.Contains(r.ValidationError, "exceed") || r.InvalidCalls != 1 || len(r.ModelTurns) != 1 {
		t.Fatalf("lost failure evidence: %+v", r)
	}
	r = EvalPreparationCase(context.Background(), c, "dynamic", prepModelFunc(func(context.Context, []Message, []Definition) (Reply, error) {
		return Reply{Calls: []Call{{ID: "broken", Name: "finish_preparation_plan", Args: json.RawMessage(`{"summary":`)}}}, nil
	}))
	if r.State != "FAILED" || len(r.ModelTurns) != 2 || r.ModelTurns[0].Calls[0].Arguments != `{"summary":` || r.Answer == "" {
		t.Fatalf("lost malformed evidence: %+v", r)
	}
	if _, err := json.Marshal(r); err != nil {
		t.Fatal("malformed arguments prevented writing report", err)
	}
}

func TestPreparationEvaluationRejectsEmptyClarificationAcrossBaselines(t *testing.T) {
	s := prepTestState()
	c := PreparationEvalCase{Input: s.Input, Sources: s.Sources, Expected: "PAUSED"}
	for _, arm := range []string{"single-prompt", "fixed-plus-synthesis"} {
		r := EvalPreparationCase(context.Background(), c, arm, prepModelFunc(func(context.Context, []Message, []Definition) (Reply, error) {
			return Reply{Calls: []Call{{ID: "empty", Name: "ask_preparation_question", Args: json.RawMessage(`{"question":""}`)}}}, nil
		}))
		if r.State != "FAILED" || r.ProgramExpectationMet || r.Error != "INVALID_QUESTION" {
			t.Fatal(arm, r)
		}
	}
}

func TestPreparationCompactGenerationPreservesAcceptedHistoricalPlans(t *testing.T) {
	state := prepTestState()
	defs := preparationDefinitionsForState(state)
	items := defs[2].Parameters["properties"].(map[string]any)["items"].(map[string]any)
	fields := items["items"].(map[string]any)["properties"].(map[string]any)
	if items["maxItems"] != 4 || fields["action"].(map[string]any)["maxLength"] != 80 || fields["references"].(map[string]any)["maxItems"] != 2 {
		t.Fatal("compact envelope missing", items)
	}
	plan := prepTestPlan(state)
	first := plan.Items[0]
	plan.Items = nil
	for i := 0; i < 5; i++ {
		item := first
		item.ID = "historical-" + string(rune('a'+i))
		item.Minutes = 10
		plan.Items = append(plan.Items, item)
	}
	if err := ValidatePreparationPlan(state, plan); err != nil {
		t.Fatal("generation hints invalidated accepted historical plan", err)
	}
}

func TestPreparationReturnedSyntaxRepairIsBoundedToOneNewTurn(t *testing.T) {
	s := prepTestState()
	calls := 0
	r := EvalPreparationCase(context.Background(), PreparationEvalCase{Input: s.Input, Sources: s.Sources, Expected: "COMPLETED"}, "dynamic", prepModelFunc(func(context.Context, []Message, []Definition) (Reply, error) {
		calls++
		return Reply{Calls: []Call{{ID: "length-limited", Name: "finish_preparation_plan", Args: json.RawMessage(`{"summary":"truncated`)}}, Usage: analysis.Usage{PromptTokens: 100, CompletionTokens: 2048, Known: true}}, nil
	}))
	if calls != 2 || r.State != "FAILED" || r.Usage.CompletionTokens != 4096 || len(r.ModelTurns) != 2 || r.InvalidCalls != 2 {
		t.Fatalf("returned syntax repair escaped its limit: %+v calls=%d", r, calls)
	}
}

func TestPreparationKnownSyntaxRepairSurvivesCheckpointWithoutUnknownRetry(t *testing.T) {
	state := prepTestState()
	v := p.AgentExecution{State: "RUNNING", Preparation: state}
	calls := 0
	model := prepModelFunc(func(_ context.Context, messages []Message, _ []Definition) (Reply, error) {
		calls++
		args := d.JSON(prepTestPlan(state))
		if calls == 1 {
			args += "}"
		} else if !strings.Contains(messages[len(messages)-1].Content, "JSON 语法") {
			t.Fatal("repair request did not change input")
		}
		return Reply{Calls: []Call{{ID: "returned", Name: "finish_preparation_plan", Args: json.RawMessage(args)}}, Usage: analysis.Usage{PromptTokens: 100, CompletionTokens: 100, Known: true}}, nil
	})
	err := RunPreparationLoop(context.Background(), "owner", &v, model, nil, func() error {
		if _, e := json.Marshal(v); e != nil {
			t.Fatal("invalid raw call reached checkpoint", e)
		}
		for _, event := range state.Trace {
			if event.Status == "INVALID_TOOL_CALL" {
				return errors.New("controlled checkpoint interruption")
			}
		}
		return nil
	})
	if err == nil || calls != 1 || state.ProviderState != "READY" || len(v.Calls) != 0 {
		t.Fatal(err, calls, state.ProviderState, v.Calls)
	}
	if err = RunPreparationLoop(context.Background(), "owner", &v, model, nil, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || v.State != "COMPLETED" {
		t.Fatal(calls, v.State)
	}
}

func TestPreparationRepeatedSourcesReuseContextAndRetainReadObservations(t *testing.T) {
	state := prepTestState()
	v := p.AgentExecution{State: "RUNNING", Preparation: state}
	calls := 0
	note := preparationSource("KNOWLEDGE", "Go 学习笔记", strings.Repeat("取消必须协作停止。", 500), "long-note", "")
	model := prepModelFunc(func(_ context.Context, messages []Message, _ []Definition) (Reply, error) {
		calls++
		if calls < 3 {
			return Reply{Calls: []Call{{ID: "read-" + string(rune('0'+calls)), Name: "search_preparation_knowledge", Args: json.RawMessage(`{"query":"Go"}`)}}}, nil
		}
		var output struct {
			Sources []d.PreparationSource `json:"sources"`
			Known   []string              `json:"already_available_source_ids"`
		}
		if json.Unmarshal([]byte(messages[len(messages)-1].Content), &output) != nil || len(output.Sources) != 0 || len(output.Known) != 1 || output.Known[0] != note.ID || strings.Contains(messages[len(messages)-1].Content, note.Text) {
			t.Fatal("repeated source body remained in context")
		}
		return Reply{Calls: []Call{{ID: "finish", Name: "finish_preparation_plan", Args: json.RawMessage(d.JSON(prepTestPlan(state)))}}}, nil
	})
	tools := prepToolsFunc(func(context.Context, string, string, json.RawMessage) (any, error) {
		return []d.PreparationSource{note}, nil
	})
	if err := RunPreparationLoop(context.Background(), "owner", &v, model, tools, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if v.State != "COMPLETED" || len(v.Observations) != 2 || len(state.Sources) != 2 || state.SearchCalls != 2 {
		t.Fatal(v.State, len(v.Observations), len(state.Sources), state.SearchCalls)
	}
}

func TestPreparationCanonicalBudgetIsSharedByAllArms(t *testing.T) {
	s := prepTestState()
	s.Input.Minutes = 15
	s.Input.Goal = "三天三小时准备"
	for _, arm := range []string{"single-prompt", "fixed-plus-synthesis", "dynamic"} {
		r := EvalPreparationCase(context.Background(), PreparationEvalCase{Input: s.Input, Sources: s.Sources, Expected: "COMPLETED"}, arm, prepModelFunc(func(_ context.Context, messages []Message, _ []Definition) (Reply, error) {
			var payload struct {
				Constraints struct {
					Minutes int `json:"total_minutes"`
				} `json:"constraints"`
			}
			if json.Unmarshal([]byte(messages[1].Content), &payload) != nil || payload.Constraints.Minutes != 15 || !strings.Contains(messages[0].Content, "优先于目标文字") {
				t.Fatal("canonical budget missing", arm)
			}
			plan := prepTestPlan(s)
			plan.Items[0].Minutes = 15
			return Reply{Calls: []Call{{ID: "finish", Name: "finish_preparation_plan", Args: json.RawMessage(d.JSON(plan))}}}, nil
		}))
		if !r.ProgramExpectationMet {
			t.Fatal(arm, r)
		}
	}
}

func TestPreparationSyntaxRepairKeepsRoomInsideToolBudget(t *testing.T) {
	state := prepTestState()
	state.ToolCalls = 7
	v := p.AgentExecution{State: "RUNNING", Preparation: state}
	calls := 0
	err := RunPreparationLoop(context.Background(), "owner", &v, prepModelFunc(func(context.Context, []Message, []Definition) (Reply, error) {
		calls++
		return Reply{Calls: []Call{{ID: "invalid", Name: "finish_preparation_plan", Args: json.RawMessage(`{"summary":`)}}}, nil
	}), nil, func() error { return nil })
	if err != nil || v.State != "FAILED" || calls != 1 || state.ToolCalls != 7 {
		t.Fatal(err, v.State, calls, state.ToolCalls)
	}
}

func TestPreparationFifteenMinutePlanPublishesFeasibleCountAndExactFeedback(t *testing.T) {
	state := prepTestState()
	state.Input.Minutes = 15
	state.Messages = json.RawMessage(d.JSON(preparationMessages(state.Input, state.Sources, nil, nil)))
	v := p.AgentExecution{State: "RUNNING", Preparation: state}
	calls := 0
	model := prepModelFunc(func(_ context.Context, messages []Message, defs []Definition) (Reply, error) {
		calls++
		items := defs[2].Parameters["properties"].(map[string]any)["items"].(map[string]any)
		if items["maxItems"] != 3 {
			t.Fatal("15 minutes cannot fit 4 items of at least 5 minutes", items)
		}
		plan := prepTestPlan(state)
		plan.Items[0].Minutes = 15
		if calls == 1 {
			plan.Items[0].Minutes = 4
		} else if !strings.Contains(messages[len(messages)-1].Content, "minutes=4 must be between 5") {
			t.Fatal("missing concrete duration feedback")
		}
		return Reply{Calls: []Call{{ID: "finish-" + string(rune('0'+calls)), Name: "finish_preparation_plan", Args: json.RawMessage(d.JSON(plan))}}}, nil
	})
	if err := RunPreparationLoop(context.Background(), "owner", &v, model, nil, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || v.State != "COMPLETED" || state.Plan.Items[0].Minutes != 15 {
		t.Fatal(calls, v.State, state.Plan)
	}
}

func TestPreparationPublishesInternalIDContractAndKeepsChineseTitles(t *testing.T) {
	state := prepTestState()
	plan := prepTestPlan(state)
	plan.Items[0].ID = "perf-measure-口径"
	plan.Items[0].Title = "性能测量口径"
	if err := ValidatePreparationPlan(state, plan); err == nil || !strings.Contains(err.Error(), "ASCII") {
		t.Fatal("ID contract not explained", err)
	}
	fields := preparationDefinitionsForState(state)[2].Parameters["properties"].(map[string]any)["items"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if fields["id"].(map[string]any)["pattern"] != preparationItemIDPattern || fields["id"].(map[string]any)["maxLength"] != 40 {
		t.Fatal("generation schema differs from validator")
	}
	plan.Items[0].ID = "perf-measure-scope"
	if err := ValidatePreparationPlan(state, plan); err != nil {
		t.Fatal("Chinese display title rejected", err)
	}
}

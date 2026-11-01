package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

type prepModelFunc func(context.Context, []Message, []Definition) (Reply, error)

func (f prepModelFunc) Next(ctx context.Context, m []Message, defs []Definition) (Reply, error) {
	return f(ctx, m, defs)
}

type prepToolsFunc func(context.Context, string, string, json.RawMessage) (any, error)

func (f prepToolsFunc) Execute(ctx context.Context, user, name string, args json.RawMessage) (any, error) {
	return f(ctx, user, name, args)
}
func (f prepToolsFunc) Definitions() []Definition { return preparationDefinitions() }
func prepTestState() *d.PreparationState {
	src := preparationSource("JD", "留档岗位", "Go 并发与 MySQL 索引", "context", strings.Repeat("a", 32))
	return &d.PreparationState{Input: d.PreparationInput{ApplicationID: strings.Repeat("a", 32), Goal: "面试准备", Minutes: 180, Days: 3}, Sources: []d.PreparationSource{src}, Messages: json.RawMessage(`[ {"role":"system","content":"policy"} ]`), Usage: json.RawMessage(`{"limit":128000}`), ProviderState: "READY"}
}
func prepTestPlan(s *d.PreparationState) d.PreparationPlan {
	return d.PreparationPlan{Summary: "优先准备并发场景", Items: []d.PreparationItem{{ID: "concurrency", Title: "Go 并发", Minutes: 60, Day: 1, Basis: "JD", Action: "解释数据竞争并写一个可取消的 worker", Deliverable: "一份可运行代码与取舍说明", References: []d.PreparationReference{{SourceID: s.Sources[0].ID, Quote: "Go 并发"}}}}}
}
func TestPreparationPlanConstraintsAndCitationBinding(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*d.PreparationState, *d.PreparationPlan)
	}{
		{"time_overrun", func(s *d.PreparationState, v *d.PreparationPlan) { v.Items[0].Minutes = 181 }},
		{"day_overrun", func(s *d.PreparationState, v *d.PreparationPlan) { v.Items[0].Day = 4 }},
		{"invented_quote", func(s *d.PreparationState, v *d.PreparationPlan) { v.Items[0].References[0].Quote = "十万QPS" }},
		{"foreign_source", func(s *d.PreparationState, v *d.PreparationPlan) {
			v.Items[0].References[0].SourceID = "another-account"
		}},
		{"wrong_basis", func(s *d.PreparationState, v *d.PreparationPlan) { v.Items[0].Basis = "HISTORY" }},
		{"source_required", func(s *d.PreparationState, v *d.PreparationPlan) { v.Items[0].References = nil }},
		{"duplicate_item", func(s *d.PreparationState, v *d.PreparationPlan) { v.Items = append(v.Items, v.Items[0]) }},
		{"mastered_must_not_reappear", func(s *d.PreparationState, v *d.PreparationPlan) { s.Input.Mastered = []string{"concurrency"} }},
		{"completed_must_not_reappear", func(s *d.PreparationState, v *d.PreparationPlan) { s.Input.Completed = []string{"concurrency"} }},
		{"needs_minutes", func(s *d.PreparationState, v *d.PreparationPlan) { s.Input.Minutes = 0 }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := prepTestState()
			v := prepTestPlan(s)
			c.mutate(s, &v)
			if ValidatePreparationPlan(s, v) == nil {
				t.Fatal("invalid plan accepted", v)
			}
		})
	}
	s := prepTestState()
	v := prepTestPlan(s)
	if err := ValidatePreparationPlan(s, v); err != nil {
		t.Fatal(err)
	}
	v.Items[0].Basis = "GENERAL"
	v.Items[0].References = nil
	if err := ValidatePreparationPlan(s, v); err != nil {
		t.Fatal("general advice needs no fabricated citation", err)
	}
	old := prepTestPlan(s)
	s.PreviousPlan = &old
	s.Input.Mastered = []string{"concurrency"}
	if err := ValidatePreparationPlan(s, d.PreparationPlan{Summary: "已完成所有事项", Items: []d.PreparationItem{}}); err != nil {
		t.Fatal(err)
	}
}
func TestPreparationFeedbackTime(t *testing.T) {
	for _, c := range []struct {
		feedback string
		want     int
	}{{"只剩一小时", 60}, {"还剩45分钟", 45}, {"只有半小时", 30}, {"剩余四十分钟", 40}, {"这项已经掌握", 180}} {
		got := NormalizePreparationInput(d.PreparationInput{Minutes: 180, Feedback: c.feedback})
		if got.Minutes != c.want {
			t.Fatal(c, got)
		}
	}
}
func TestPreparationDynamicRewriteAndPlan(t *testing.T) {
	state := prepTestState()
	v := p.AgentExecution{State: "RUNNING", Preparation: state}
	modelCalls, tools, checkpoints := 0, 0, 0
	model := prepModelFunc(func(ctx context.Context, msg []Message, defs []Definition) (Reply, error) {
		modelCalls++
		name, args := "search_preparation_knowledge", `{"query":"unknown-term"}`
		if modelCalls == 2 {
			args = `{"query":"Go 并发"}`
		}
		if modelCalls == 3 {
			name = "finish_preparation_plan"
			args = d.JSON(prepTestPlan(state))
		}
		return Reply{Calls: []Call{{ID: "call-" + strings.Repeat("x", modelCalls), Name: name, Args: json.RawMessage(args)}}, Usage: analysis.Usage{Known: true, PromptTokens: 100, CompletionTokens: 20}}, nil
	})
	toolset := prepToolsFunc(func(ctx context.Context, user, name string, args json.RawMessage) (any, error) {
		tools++
		return []d.PreparationSource{}, nil
	})
	if err := RunPreparationLoop(context.Background(), "owner", &v, model, toolset, func() error { checkpoints++; return nil }); err != nil {
		t.Fatal(err)
	}
	if v.State != "COMPLETED" || modelCalls != 3 || tools != 2 || v.Next != 3 || checkpoints < 8 {
		t.Fatal(v, modelCalls, tools, checkpoints)
	}
	if v.Preparation.Plan == nil || v.Preparation.SearchCalls != 2 {
		t.Fatal(v)
	}
}
func TestPreparationUnknownModelAndDurablePendingTools(t *testing.T) {
	t.Run("unknown_model_no_internal_retry", func(t *testing.T) {
		v := p.AgentExecution{State: "RUNNING", Preparation: prepTestState()}
		calls := 0
		err := RunPreparationLoop(context.Background(), "owner", &v, prepModelFunc(func(context.Context, []Message, []Definition) (Reply, error) {
			calls++
			return Reply{}, errors.New("provider disconnected")
		}), nil, func() error { return nil })
		if err == nil || calls != 1 || v.State != "PAUSED" || v.Preparation.ProviderState != "UNKNOWN" {
			t.Fatal(v, err, calls)
		}
	})
	t.Run("resume_completed_reads_without_replay", func(t *testing.T) {
		state := prepTestState()
		v := p.AgentExecution{State: "RUNNING", Preparation: state, Next: 1, Calls: []p.ExecutionCall{{ID: "done", Name: "search_preparation_knowledge", Args: json.RawMessage(`{"query":"already-read"}`)}, {ID: "pending", Name: "search_preparation_knowledge", Args: json.RawMessage(`{"query":"Go"}`)}}}
		tools := 0
		toolset := prepToolsFunc(func(ctx context.Context, user, name string, args json.RawMessage) (any, error) {
			if strings.Contains(string(args), "already-read") {
				t.Fatal("completed read repeated")
			}
			tools++
			return []d.PreparationSource{}, nil
		})
		model := prepModelFunc(func(context.Context, []Message, []Definition) (Reply, error) {
			return Reply{Calls: []Call{{ID: "finish", Name: "finish_preparation_plan", Args: json.RawMessage(d.JSON(prepTestPlan(state)))}}}, nil
		})
		if err := RunPreparationLoop(context.Background(), "owner", &v, model, toolset, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
		if tools != 1 || v.State != "COMPLETED" {
			t.Fatal(v, tools)
		}
	})
}
func TestPreparationBoundsAndClarification(t *testing.T) {
	t.Run("six_model_calls", func(t *testing.T) {
		v := p.AgentExecution{State: "RUNNING", Preparation: prepTestState()}
		model := prepModelFunc(func(context.Context, []Message, []Definition) (Reply, error) { return Reply{Text: "just prose"}, nil })
		if err := RunPreparationLoop(context.Background(), "owner", &v, model, nil, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
		if v.Preparation.ModelCalls != 6 || v.State != "FAILED" {
			t.Fatal(v)
		}
	})
	t.Run("ask_instead_of_inventing", func(t *testing.T) {
		s := prepTestState()
		s.Input.Minutes = 0
		v := p.AgentExecution{State: "RUNNING", Preparation: s}
		model := prepModelFunc(func(context.Context, []Message, []Definition) (Reply, error) {
			return Reply{Calls: []Call{{ID: "ask", Name: "ask_preparation_question", Args: json.RawMessage(`{"question":"你总共还有多少分钟准备？"}`)}}}, nil
		})
		if err := RunPreparationLoop(context.Background(), "owner", &v, model, nil, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
		if v.State != "PAUSED" || v.Preparation.Question == "" || v.Preparation.Plan != nil {
			t.Fatal(v)
		}
	})
	t.Run("write_tool_never_executed", func(t *testing.T) {
		s := prepTestState()
		v := p.AgentExecution{State: "RUNNING", Preparation: s}
		n := 0
		model := prepModelFunc(func(context.Context, []Message, []Definition) (Reply, error) {
			n++
			if n == 1 {
				return Reply{Calls: []Call{{ID: "write", Name: "save_preparation_plan", Args: json.RawMessage(`{}`)}}}, nil
			}
			return Reply{Calls: []Call{{ID: "finish", Name: "finish_preparation_plan", Args: json.RawMessage(d.JSON(prepTestPlan(s)))}}}, nil
		})
		if err := RunPreparationLoop(context.Background(), "owner", &v, model, prepToolsFunc(func(context.Context, string, string, json.RawMessage) (any, error) {
			t.Fatal("write escaped whitelist")
			return nil, nil
		}), func() error { return nil }); err != nil {
			t.Fatal(err)
		}
		if v.State != "COMPLETED" || len(v.Preparation.Trace) == 0 {
			t.Fatal(v)
		}
	})
	t.Run("cancel_propagates_to_model", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		v := p.AgentExecution{State: "RUNNING", Preparation: prepTestState()}
		err := RunPreparationLoop(ctx, "owner", &v, prepModelFunc(func(ctx context.Context, _ []Message, _ []Definition) (Reply, error) {
			<-ctx.Done()
			return Reply{}, ctx.Err()
		}), nil, func() error { return nil })
		if !errors.Is(err, context.DeadlineExceeded) || v.Preparation.ProviderState != "UNKNOWN" {
			t.Fatal(v, err)
		}
	})
}

func TestPreparationReviewedEvaluationMatrix(t *testing.T) {
	raw, err := os.ReadFile("testdata/preparation-tasks.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []PreparationEvalCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	splits := map[string]int{}
	if len(cases) < 20 || len(cases) > 30 {
		t.Fatal("reviewable matrix size", len(cases))
	}
	for _, c := range cases {
		if ids[c.ID] || c.MaterialKind != "synthetic-control" || c.ReviewNote == "" {
			t.Fatal("unreviewable/duplicate control", c.ID)
		}
		ids[c.ID] = true
		splits[c.Split]++
		for _, arm := range []string{"single-prompt", "fixed-plus-synthesis", "dynamic"} {
			t.Run(c.ID+"/"+arm, func(t *testing.T) {
				result := EvalPreparationCase(context.Background(), c, arm, &ControlledPreparationModel{Control: c.Control})
				if !result.ProgramExpectationMet {
					t.Fatalf("controlled regression: %+v", result)
				}
			})
		}
	}
	if splits["development"] != 6 || splits["validation"] != 18 {
		t.Fatal(splits)
	}
}

func TestPreparationDispatchReservesUnknownBudgetBeforeExternalCall(t *testing.T) {
	state := prepTestState()
	v := p.AgentExecution{State: "RUNNING", Preparation: state}
	dispatched := false
	err := RunPreparationLoop(context.Background(), "owner", &v, prepModelFunc(func(context.Context, []Message, []Definition) (Reply, error) {
		t.Fatal("checkpoint crash should prevent dispatch")
		return Reply{}, nil
	}), nil, func() error {
		if state.ProviderState == "DISPATCHED" {
			var budget TokenBudget
			if json.Unmarshal(state.Usage, &budget) != nil || budget.Charged <= 0 || budget.UnreportedCalls != 1 {
				t.Fatal("no durable reservation", string(state.Usage))
			}
			dispatched = true
			return errors.New("controlled process interruption")
		}
		return nil
	})
	if err == nil || !dispatched || state.ModelCalls != 1 {
		t.Fatal(v, err)
	}
}

func TestPreparationTerminalBatchDoesNotLeaveHiddenPendingTools(t *testing.T) {
	state := prepTestState()
	v := p.AgentExecution{State: "RUNNING", Preparation: state}
	calls := 0
	model := prepModelFunc(func(context.Context, []Message, []Definition) (Reply, error) {
		calls++
		finish := Call{ID: "finish", Name: "finish_preparation_plan", Args: json.RawMessage(d.JSON(prepTestPlan(state)))}
		if calls == 1 {
			return Reply{Calls: []Call{{ID: "read", Name: "search_preparation_knowledge", Args: json.RawMessage(`{"query":"Go"}`)}, finish}}, nil
		}
		return Reply{Calls: []Call{finish}}, nil
	})
	err := RunPreparationLoop(context.Background(), "owner", &v, model, prepToolsFunc(func(context.Context, string, string, json.RawMessage) (any, error) {
		t.Fatal("mixed terminal batch ran an unnecessary query")
		return nil, nil
	}), func() error { return nil })
	if err != nil || v.State != "COMPLETED" || v.Next != len(v.Calls) || calls != 2 {
		t.Fatal(v, err, calls)
	}
}

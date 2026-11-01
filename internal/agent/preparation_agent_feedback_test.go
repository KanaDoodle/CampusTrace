package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func citationRepairFixture() (*d.PreparationState, d.PreparationPlan) {
	state := prepTestState()
	project := preparationSource("PROJECT", "当前项目材料", "后台任务使用 Kafka。", "project", "")
	resume := preparationSource("RESUME", "本次简历快照", "合成实验：累计分配量减少42%。", "resume", state.Input.ApplicationID)
	state.Sources = append(state.Sources, project, resume)
	plan := prepTestPlan(state)
	plan.Items[0].References = append(plan.Items[0].References, d.PreparationReference{SourceID: project.ID, Quote: resume.Text})
	return state, plan
}

func TestPreparationCitationFeedbackLocatesExactAlreadyReadSourceWithoutRebinding(t *testing.T) {
	state, plan := citationRepairFixture()
	err := ValidatePreparationPlan(state, plan)
	if !errors.Is(err, p.ErrValidation) {
		t.Fatal("reference mismatch not rejected", err)
	}
	repair := preparationPlanRepair(state, plan, err)
	raw := json.RawMessage(d.JSON(repair))
	var feedback struct {
		Citation struct {
			Item      int `json:"item_index"`
			Reference int `json:"reference_index"`
			Matches   []struct {
				ID   string `json:"source_id"`
				Kind string `json:"kind"`
			} `json:"matching_sources"`
		} `json:"citation"`
	}
	if json.Unmarshal(raw, &feedback) != nil || len(feedback.Citation.Matches) != 1 || feedback.Citation.Matches[0].ID != state.Sources[2].ID || feedback.Citation.Matches[0].Kind != "RESUME" || feedback.Citation.Reference != 1 {
		t.Fatal("wrong repair candidates", string(raw))
	}
	if plan.Items[0].References[1].SourceID != state.Sources[1].ID || ValidatePreparationPlan(state, plan) == nil {
		t.Fatal("feedback silently rebound a reference")
	}
	plan.Items[0].References[1].SourceID = feedback.Citation.Matches[0].ID
	if err = ValidatePreparationPlan(state, plan); err != nil {
		t.Fatal("explicit valid selection rejected", err)
	}
}

func TestPreparationCitationRepairRequiresExplicitNewModelChoice(t *testing.T) {
	state, plan := citationRepairFixture()
	state.Messages = json.RawMessage(d.JSON(preparationMessages(state.Input, state.Sources, nil, nil)))
	v := p.AgentExecution{State: "RUNNING", Preparation: state}
	calls := 0
	model := prepModelFunc(func(_ context.Context, messages []Message, _ []Definition) (Reply, error) {
		calls++
		if calls == 2 {
			var feedback struct {
				Repair struct {
					Citation struct {
						Matches []struct {
							ID string `json:"source_id"`
						} `json:"matching_sources"`
					} `json:"citation"`
				} `json:"repair"`
			}
			if json.Unmarshal([]byte(messages[len(messages)-1].Content), &feedback) != nil || len(feedback.Repair.Citation.Matches) != 1 {
				t.Fatal("structured source feedback missing")
			}
			plan.Items[0].References[1].SourceID = feedback.Repair.Citation.Matches[0].ID
		}
		return Reply{Calls: []Call{{ID: "plan-" + string(rune('0'+calls)), Name: "finish_preparation_plan", Args: json.RawMessage(d.JSON(plan))}}}, nil
	})
	if err := RunPreparationLoop(context.Background(), "owner", &v, model, nil, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || v.State != "COMPLETED" || state.Plan.Items[0].References[1].SourceID != state.Sources[2].ID {
		t.Fatal(calls, v.State, state.Plan)
	}
	if len(v.Calls) != 2 || v.Next != 2 || state.ToolCalls != 2 {
		t.Fatal("rejected and completed steps not retained", v.Calls, v.Next, state.ToolCalls)
	}
}

func TestPreparationCitationRepairDoesNotUseUnreadOrApproximateMaterial(t *testing.T) {
	state, plan := citationRepairFixture()
	state.Sources = state.Sources[:2] // Matching résumé is not available to the execution.
	err := ValidatePreparationPlan(state, plan)
	var feedback struct {
		Citation struct {
			Matches []any `json:"matching_sources"`
		} `json:"citation"`
	}
	if json.Unmarshal([]byte(d.JSON(preparationPlanRepair(state, plan, err))), &feedback) != nil || len(feedback.Citation.Matches) != 0 {
		t.Fatal("unread source became a repair candidate")
	}
	plan.Items[0].References[1].Quote = "后台任务使用Kafka。" // Original has spaces.
	err = ValidatePreparationPlan(state, plan)
	if err == nil {
		t.Fatal("fuzzy quotation accepted")
	}
	if json.Unmarshal([]byte(d.JSON(preparationPlanRepair(state, plan, err))), &feedback) != nil || len(feedback.Citation.Matches) != 0 {
		t.Fatal("approximate quote offered as exact evidence")
	}
}

func TestPreparationEvaluationRecordsCorrectionGivenToNextModelTurn(t *testing.T) {
	state, plan := citationRepairFixture()
	calls := 0
	r := EvalPreparationCase(context.Background(), PreparationEvalCase{Input: state.Input, Sources: state.Sources, Expected: "COMPLETED"}, "dynamic", prepModelFunc(func(context.Context, []Message, []Definition) (Reply, error) {
		calls++
		if calls == 2 {
			plan.Items[0].References[1].SourceID = state.Sources[2].ID
		}
		return Reply{Calls: []Call{{ID: "finish-" + string(rune('0'+calls)), Name: "finish_preparation_plan", Args: json.RawMessage(d.JSON(plan))}}}, nil
	}))
	if !r.ProgramExpectationMet || len(r.ModelTurns) != 2 || len(r.ModelTurns[1].ProgramFeedback) != 1 {
		t.Fatal("correction evidence not retained", r.State, r.ModelTurns)
	}
	var feedback struct {
		Repair struct {
			Citation struct {
				Matches []struct {
					ID string `json:"source_id"`
				} `json:"matching_sources"`
			} `json:"citation"`
		} `json:"repair"`
	}
	if json.Unmarshal(r.ModelTurns[1].ProgramFeedback[0], &feedback) != nil || len(feedback.Repair.Citation.Matches) != 1 || feedback.Repair.Citation.Matches[0].ID != state.Sources[2].ID {
		t.Fatal("wrong correction evidence")
	}
}

func TestPreparationClarificationScoreRejectsUnknownProviderOutcome(t *testing.T) {
	state := prepTestState()
	state.Input.Minutes = 0
	for _, arm := range []string{"single-prompt", "fixed-plus-synthesis", "dynamic"} {
		r := EvalPreparationCase(context.Background(), PreparationEvalCase{Input: state.Input, Sources: state.Sources, Expected: "PAUSED"}, arm, prepModelFunc(func(context.Context, []Message, []Definition) (Reply, error) {
			return Reply{}, errors.New("controlled provider disconnect")
		}))
		if r.State != "PAUSED" || r.Question != "" || r.ProgramExpectationMet {
			t.Fatalf("provider interruption counted as clarification: arm=%s state=%s met=%v", arm, r.State, r.ProgramExpectationMet)
		}
	}
}

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func qualityFixture() (*d.PreparationState, d.PreparationPlan) {
	s := prepTestState()
	s.QualityVersion = PreparationQualityVersion
	plan := prepTestPlan(s)
	plan.Items[0].Workload = &d.PreparationWorkload{Mode: "CODE", Units: 1, Scope: "1个示例：Go取消及基本测试"}
	plan.Items[0].References[0].Supports = "岗位要求Go并发，对应取消传播练习。"
	return s, plan
}
func TestPreparationQualityRejectsOversizedWorkloadAndUnexplainedCitations(t *testing.T) {
	for _, name := range []string{"three_programs_in_35_minutes", "no_support_explanation", "code_in_fifteen_minutes"} {
		t.Run(name, func(t *testing.T) {
			s, plan := qualityFixture()
			switch name {
			case "three_programs_in_35_minutes":
				plan.Items[0].Minutes = 35
				plan.Items[0].Workload.Units = 3
				plan.Items[0].Workload.Scope = "3道小题及基本测试"
			case "no_support_explanation":
				plan.Items[0].References[0].Supports = ""
			case "code_in_fifteen_minutes":
				s.Input.Minutes = 15
				plan.Items[0].Minutes = 15
			}
			if ValidatePreparationPlan(s, plan) == nil {
				t.Fatal("unbounded or unexplained plan accepted")
			}
		})
	}
}
func TestPreparationQualityRepairsWorkloadBeforeCompleting(t *testing.T) {
	s, plan := qualityFixture()
	s.Input.Minutes = 35
	plan.Items[0].Minutes = 35
	plan.Items[0].Workload.Units = 3
	plan.Items[0].Workload.Scope = "3道小题及基本测试"
	v := p.AgentExecution{State: "RUNNING", Preparation: s}
	calls := 0
	model := prepModelFunc(func(_ context.Context, messages []Message, _ []Definition) (Reply, error) {
		calls++
		if calls == 2 {
			if !strings.Contains(messages[len(messages)-1].Content, "WORKLOAD") {
				t.Fatal("missing workload feedback")
			}
			plan.Items[0].Workload.Units = 1
			plan.Items[0].Workload.Scope = "1道小题及基本测试"
		}
		return Reply{Calls: []Call{{ID: "finish-" + strings.Repeat("x", calls), Name: "finish_preparation_plan", Args: json.RawMessage(d.JSON(plan))}}}, nil
	})
	if err := RunPreparationLoop(context.Background(), "owner", &v, model, nil, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if v.State != "COMPLETED" || calls != 2 {
		t.Fatal(v.State, calls)
	}
}
func TestPreparationQualityShortTaskDoesNotExecuteTwoSearches(t *testing.T) {
	s, plan := qualityFixture()
	s.Input.Minutes = 15
	plan.Items[0].Minutes = 15
	plan.Items[0].Workload.Mode = "ORAL"
	v := p.AgentExecution{State: "RUNNING", Preparation: s}
	models, reads := 0, 0
	model := prepModelFunc(func(context.Context, []Message, []Definition) (Reply, error) {
		models++
		if models == 1 {
			calls := []Call{}
			for _, id := range []string{"a", "b"} {
				calls = append(calls, Call{ID: id, Name: "search_preparation_knowledge", Args: json.RawMessage(`{"query":"Go","gap":"缺少取消示例的核对点","expected_use":"说明口述中的取消边界"}`)})
			}
			return Reply{Calls: calls}, nil
		}
		return Reply{Calls: []Call{{ID: "finish", Name: "finish_preparation_plan", Args: json.RawMessage(d.JSON(plan))}}}, nil
	})
	tools := prepToolsFunc(func(context.Context, string, string, json.RawMessage) (any, error) {
		reads++
		return []d.PreparationSource{}, nil
	})
	if err := RunPreparationLoop(context.Background(), "owner", &v, model, tools, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if reads != 1 || s.SearchCalls != 1 || v.State != "COMPLETED" {
		t.Fatal("short task read bound", reads, s.SearchCalls, v.State)
	}
}

func TestPreparationQualityReadOffAndNoNewEvidence(t *testing.T) {
	s, plan := qualityFixture()
	s.Input.RetrievalMode = "off"
	for _, def := range preparationDefinitionsForState(s) {
		if strings.HasPrefix(def.Name, "search_") {
			t.Fatal("off mode exposed search")
		}
	}
	v := p.AgentExecution{State: "RUNNING", Preparation: s}
	models, reads := 0, 0
	model := prepModelFunc(func(context.Context, []Message, []Definition) (Reply, error) {
		models++
		if models == 1 {
			return Reply{Calls: []Call{{ID: "forbidden", Name: "search_preparation_knowledge", Args: json.RawMessage(`{"query":"Go","gap":"x","expected_use":"y"}`)}}}, nil
		}
		return Reply{Calls: []Call{{ID: "finish", Name: "finish_preparation_plan", Args: json.RawMessage(d.JSON(plan))}}}, nil
	})
	tools := prepToolsFunc(func(context.Context, string, string, json.RawMessage) (any, error) {
		reads++
		return []d.PreparationSource{}, nil
	})
	if err := RunPreparationLoop(context.Background(), "owner", &v, model, tools, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if reads != 0 || v.State != "COMPLETED" {
		t.Fatal("off mode performed reads", reads, v.State)
	}
	s.Input.RetrievalMode = "auto"
	s.Retrievals = []d.PreparationRetrieval{{Outcome: "EMPTY"}, {Outcome: "SUCCESS", NewSourceIDs: []string{}}}
	if !preparationNoNewEvidence(s) {
		t.Fatal("repeated no-new-evidence not bounded")
	}
	s.Retrievals = append(s.Retrievals, d.PreparationRetrieval{Outcome: "SUCCESS", NewSourceIDs: []string{"new"}})
	if preparationNoNewEvidence(s) {
		t.Fatal("new evidence not distinguished")
	}
}

func TestPreparationQualityTraceRecordsEvidenceUse(t *testing.T) {
	s, plan := qualityFixture()
	note := preparationSource("KNOWLEDGE", "取消协作", "Go 取消需要协作停止。", "note", "")
	plan.Items[0].References = append(plan.Items[0].References, d.PreparationReference{SourceID: note.ID, Quote: note.Text, Supports: "说明取消示例必须协作退出。"})
	v := p.AgentExecution{State: "RUNNING", Preparation: s}
	calls := 0
	model := prepModelFunc(func(context.Context, []Message, []Definition) (Reply, error) {
		calls++
		if calls == 1 {
			return Reply{Calls: []Call{{ID: "read", Name: "search_preparation_knowledge", Args: json.RawMessage(`{"query":"取消","gap":"缺少取消边界核对点","expected_use":"核对已有示例的退出条件"}`)}}}, nil
		}
		return Reply{Calls: []Call{{ID: "finish", Name: "finish_preparation_plan", Args: json.RawMessage(d.JSON(plan))}}}, nil
	})
	tools := prepToolsFunc(func(_ context.Context, _ string, _ string, args json.RawMessage) (any, error) {
		if strings.Contains(string(args), "gap") {
			t.Fatal("planning metadata sent to business tool")
		}
		return []d.PreparationSource{note}, nil
	})
	if err := RunPreparationLoop(context.Background(), "owner", &v, model, tools, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(s.Retrievals) != 1 || s.Retrievals[0].Step != 0 || len(s.Retrievals[0].NewSourceIDs) != 1 || len(s.Retrievals[0].CitedSourceIDs) != 1 || s.Retrievals[0].Gap == "" {
		t.Fatal(s.Retrievals)
	}
	// Historical plans without the new version remain readable and valid.
	s.QualityVersion = ""
	plan.Items[0].Workload = nil
	for i := range plan.Items[0].References {
		plan.Items[0].References[i].Supports = ""
	}
	if err := ValidatePreparationPlan(s, plan); err != nil {
		t.Fatal("historical plan rejected", err)
	}
}

func TestPreparationQualityRejectsHiddenScopeAndReportsAllWorkloads(t *testing.T) {
	s, plan := qualityFixture()
	s.Input.Minutes = 15
	plan.Items[0].Minutes = 8
	plan.Items[0].Workload = &d.PreparationWorkload{Mode: "ORAL", Units: 1, Scope: "3个模式各复述一次"}
	if err := ValidatePreparationPlan(s, plan); err == nil {
		t.Fatal("three topics disguised as one unit")
	}
	second := plan.Items[0]
	second.ID = "network"
	second.Minutes = 7
	second.Workload = &d.PreparationWorkload{Mode: "ORAL", Units: 2, Scope: "2个网络问题"}
	plan.Items = append(plan.Items, second)
	repair := preparationPlanRepair(s, plan, ValidatePreparationPlan(s, plan))
	rows, ok := repair["workloads"].([]map[string]any)
	if !ok || len(rows) != 2 || rows[0]["scope_declared_count"] != 3 || rows[1]["maximum_units_in_item_minutes"] != 1 {
		t.Fatal("partial feedback", repair)
	}
}
func TestPreparationEvaluationLowCallCapStopsBeforeProviderDispatch(t *testing.T) {
	s, _ := qualityFixture()
	calls := 0
	model := prepModelFunc(func(context.Context, []Message, []Definition) (Reply, error) {
		calls++
		return Reply{Text: "please continue"}, nil
	})
	result := EvalPreparationGraphCaseWithLimit(context.Background(), PreparationEvalCase{Input: s.Input, Sources: s.Sources, Expected: "COMPLETED"}, "dynamic", model, nil, 1)
	if calls != 1 || result.Calls != 1 || result.State != "FAILED" || result.ProgramExpectationMet {
		t.Fatal("call admission exceeded or mis-scored", calls, result.State, result.Calls)
	}
	for _, trace := range result.Trace {
		if trace.Status == "UNKNOWN" {
			t.Fatal("undispatched budget failure labelled unknown")
		}
	}
}

func TestPreparationQualityStopsAfterTwoReadsWithoutNewEvidence(t *testing.T) {
	s, plan := qualityFixture()
	v := p.AgentExecution{State: "RUNNING", Preparation: s}
	models, reads := 0, 0
	model := prepModelFunc(func(context.Context, []Message, []Definition) (Reply, error) {
		models++
		if models == 1 {
			calls := []Call{}
			for _, id := range []string{"a", "b", "c"} {
				calls = append(calls, Call{ID: id, Name: "search_preparation_knowledge", Args: json.RawMessage(`{"query":"missing","gap":"缺少具体核对点","expected_use":"用于本项自查"}`)})
			}
			return Reply{Calls: calls}, nil
		}
		return Reply{Calls: []Call{{ID: "finish", Name: "finish_preparation_plan", Args: json.RawMessage(d.JSON(plan))}}}, nil
	})
	tools := prepToolsFunc(func(context.Context, string, string, json.RawMessage) (any, error) {
		reads++
		return []d.PreparationSource{}, nil
	})
	if err := RunPreparationLoop(context.Background(), "owner", &v, model, tools, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if reads != 2 || s.SearchCalls != 2 || v.State != "COMPLETED" {
		t.Fatal(reads, s.SearchCalls, v.State)
	}
}

func TestPreparationRetrievalAuditUsesDurableStepRatherThanProviderCallID(t *testing.T) {
	s, plan := qualityFixture()
	v := p.AgentExecution{State: "RUNNING", Preparation: s}
	models, reads := 0, 0
	model := prepModelFunc(func(context.Context, []Message, []Definition) (Reply, error) {
		models++
		if models <= 2 {
			return Reply{Calls: []Call{{ID: "provider-reused-id", Name: "search_preparation_knowledge", Args: json.RawMessage(`{"query":"Go","gap":"补查一个核对点","expected_use":"口述自查"}`)}}}, nil
		}
		return Reply{Calls: []Call{{ID: "finish", Name: "finish_preparation_plan", Args: json.RawMessage(d.JSON(plan))}}}, nil
	})
	tools := prepToolsFunc(func(context.Context, string, string, json.RawMessage) (any, error) {
		reads++
		return []d.PreparationSource{preparationSource("KNOWLEDGE", "笔记", strings.Repeat("Go", reads), strings.Repeat("x", reads), "")}, nil
	})
	if err := RunPreparationLoop(context.Background(), "owner", &v, model, tools, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(s.Retrievals) != 2 || s.Retrievals[0].Step != 0 || s.Retrievals[1].Step != 1 {
		t.Fatal("provider ID merged independent reads", s.Retrievals)
	}
}

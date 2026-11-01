package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type preparationBaselineModel struct{ turn int }

func (m *preparationBaselineModel) Next(_ context.Context, _ []Message, _ []Definition) (Reply, error) {
	m.turn++
	if m.turn == 1 {
		return Reply{Calls: []Call{{ID: "frozen", Name: "get_application_dossier", Args: json.RawMessage(`{"application_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)}}}, nil
	}
	return Reply{Text: "180分钟计划：Go 并发60分钟，MySQL60分钟，项目讲解60分钟。"}, nil
}

type preparationBaselineTools struct{}

func (preparationBaselineTools) Definitions() []Definition {
	return []Definition{{Name: "get_application_dossier", Parameters: object(map[string]any{"application_id": str()}, "application_id")}}
}
func (preparationBaselineTools) Execute(context.Context, string, string, json.RawMessage) (any, error) {
	return map[string]any{"application_id": strings.Repeat("a", 32), "context_key": strings.Repeat("b", 64), "phase": "SUBMITTED", "job": map[string]string{"company": "Synthetic", "title": "Go 后端"}, "captured_at": "2026-10-30T00:00:00Z", "jd": "熟悉 Go、MySQL", "candidate_document": "实现 Go 服务"}, nil
}
func TestPreparationUpgradeBaseline(t *testing.T) {
	model := &preparationBaselineModel{}
	r := Runtime{Model: model, Tools: preparationBaselineTools{}, MaxModels: 4, MaxTools: 4, Deadline: time.Second, ToolTimeout: time.Second}
	got := r.Run(context.Background(), "synthetic-user", "prep-baseline", "三天后面试本次申请，只有三小时，请安排准备计划", nil)
	if got.Terminal != "COMPLETED" || got.Executed != 1 {
		t.Fatal(got)
	}
	if strings.Contains(got.Answer, "180分钟") {
		t.Fatal("baseline has changed; review comparison", got.Answer)
	}
	fixed := SkillInput{SkillID: "application-prep", ApplicationID: strings.Repeat("a", 32), RequestKey: "baseline-task"}.Plan()
	if len(fixed) != 1 || fixed[0].Name != "get_application_preparation" {
		t.Fatal(fixed)
	}
	t.Log(`{"evidence":"controlled-response-baseline","dynamic_tools_present":true,"frozen_read_present":true,"model_time_plan_preserved":false,"fixed_workflow_calls":1,"fixed_workflow_has_time_or_feedback_input":false}`)
}

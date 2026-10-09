package agent

import (
	"strings"
	"testing"
)

func TestMatchingToolArgumentsAreScopedAndBounded(t *testing.T) {
	id := strings.Repeat("a", 32)
	other := strings.Repeat("b", 32)
	for _, sample := range []struct {
		name  string
		args  string
		valid bool
	}{
		{"get_match_result", `{"job_id":"` + id + `"}`, true},
		{"get_match_result", `{"job_id":"` + id + `","model":"other"}`, false},
		{"compare_company_jobs", `{"company":"示例公司"}`, true},
		{"compare_company_jobs", `{"job_ids":["` + id + `","` + other + `"]}`, true},
		{"compare_company_jobs", `{"job_ids":["` + id + `"]}`, false},
		{"compare_company_jobs", `{"job_ids":["` + id + `","` + id + `"]}`, false},
		{"compare_company_jobs", `{"company":"示例公司","user_id":"someone-else"}`, false},
		{"get_match_tasks", `{}`, true},
		{"get_match_tasks", `{"user_id":"someone-else"}`, false},
	} {
		if got := Validate(sample.name, []byte(sample.args)) == nil; got != sample.valid {
			t.Errorf("%s %s: valid=%t", sample.name, sample.args, got)
		}
	}
}

func TestMatchingAnswerUsesCurrentProjectedEvidenceOnly(t *testing.T) {
	result := GroundedAnswer([]any{map[string]any{"tool": "get_match_result", "data": map[string]any{
		"job_id": "job", "company": "公司", "title": "后端开发", "state": "ANALYZED", "score": 85.0, "coverage": 100.0,
		"eligibility": "ELIGIBLE", "job_status": "OPEN", "strengths": []any{map[string]any{"requirement_id": "r1", "requirement": "Go", "result": "DIRECT", "fact_id": "f1"}},
	}}})
	for _, want := range []string{"后端开发", "85.0", "要求 r1", "资料 f1"} {
		if !strings.Contains(result, want) {
			t.Fatalf("missing %s: %s", want, result)
		}
	}
	stale := GroundedAnswer([]any{map[string]any{"tool": "get_match_result", "data": map[string]any{
		"job_id": "job", "company": "公司", "title": "后端开发", "state": "STALE", "notice": "请更新分析", "score": 85.0,
	}}})
	if strings.Contains(stale, "85") || !strings.Contains(stale, "请更新分析") {
		t.Fatal("stale score leaked", stale)
	}
	comparison := GroundedAnswer([]any{map[string]any{"tool": "compare_company_jobs", "data": map[string]any{
		"company": "公司", "scope": "SELECTED", "total": 3, "analyzed": 2, "pending": 1, "stale": 0,
		"recommended_count": 1, "shown": 3, "jobs": []any{map[string]any{"job_id": "a", "title": "A", "state": "ANALYZED", "recommended": true, "score": 80.0, "coverage": 100.0}},
	}}})
	if !strings.Contains(comparison, "Job a") || !strings.Contains(comparison, "不能视为全公司最终排序") {
		t.Fatal(comparison)
	}
}

func TestWholeGroundingUsesQualitativeFitAndNamedRelativeChoices(t *testing.T) {
	answer := GroundedAnswer([]any{map[string]any{"tool": "get_match_result", "data": map[string]any{"job_id": "a", "company": "示例公司", "title": "服务端开发", "state": "ANALYZED", "mode": "holistic-v1", "fit": "RELATED", "summary": "服务实践相关，可考虑投递", "core_work": "服务开发", "strengths": []any{map[string]any{"point": "可靠性经验", "explanation": "保持实践范围"}}, "score": 100.0, "coverage": 100.0}}})
	if !strings.Contains(answer, "整体适配 值得考虑") || !strings.Contains(answer, "可靠性经验") || strings.Contains(answer, "100") {
		t.Fatal("whole report invented numeric score", answer)
	}
	answer = GroundedAnswer([]any{map[string]any{"tool": "compare_company_jobs", "data": map[string]any{"company": "示例公司", "mode": "holistic-v1", "total": 2, "summary": "仅本次候选范围", "choices": []any{map[string]any{"job_id": "a", "title": "服务端开发", "rank": 1, "reason": "核心工作相近", "advantage": "服务实践", "tradeoff": "规模需要核实"}}, "notice": "未覆盖官网全部岗位"}}})
	for _, want := range []string{"服务端开发", "第 1 组", "规模需要核实", "未覆盖官网全部岗位"} {
		if !strings.Contains(answer, want) {
			t.Fatal(want, answer)
		}
	}
	if strings.Contains(answer, "评分") || strings.Contains(answer, "覆盖 100") {
		t.Fatal(answer)
	}
}

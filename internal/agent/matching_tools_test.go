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

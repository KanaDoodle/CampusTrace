package matching

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

type inspectingComparisonModel struct {
	fakeModel
	input string
}

func (m *inspectingComparisonModel) Complete(ctx context.Context, messages, tools any) (json.RawMessage, error) {
	var msgs []map[string]string
	if err := json.Unmarshal([]byte(d.JSON(messages)), &msgs); err != nil {
		return nil, err
	}
	m.input = msgs[1]["content"]
	return m.fakeModel.Complete(ctx, messages, tools)
}

func TestComparisonSeparatesLimitationsAndOmitsPreferencesWithoutChangingCandidate(t *testing.T) {
	c := Candidate{Revision: 2, Facts: []Fact{{ID: "go", Kind: "LANGUAGE", Text: "Go"}, {ID: "implemented", Kind: "IMPLEMENTED", Text: "实现任务重试", ProjectName: "任务队列"}, {ID: "limit", Kind: "LIMITATION", Text: "没有生产环境经验"}, {ID: "role", Kind: "ROLE", Text: "后端开发"}, {ID: "city", Kind: "CITY_PREFERRED", Text: "上海"}, {ID: "type", Kind: "JOB_TYPE_PREFERENCE", Text: "FULL_TIME"}}}
	before := d.JSON(c)
	model := &inspectingComparisonModel{fakeModel: fakeModel{output: map[string]any{"jobs": []any{map[string]any{"job_id": "j", "matches": []Match{}}}}}}
	_, err := Compare(context.Background(), model, c, []MatchInput{{ID: "j", Requirements: []Requirement{}}})
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		Candidate comparisonModelCandidate `json:"candidate"`
	}
	if err := json.Unmarshal([]byte(model.input), &input); err != nil {
		t.Fatal(err)
	}
	if len(input.Candidate.Facts) != 2 || len(input.Candidate.Limitations) != 1 || input.Candidate.Facts[1].ProjectName != "任务队列" || input.Candidate.Limitations[0].ID != "limit" || input.Candidate.Revision != 2 {
		t.Fatal(input)
	}
	for _, kind := range []string{"ROLE", "CITY_PREFERRED", "JOB_TYPE_PREFERENCE"} {
		if strings.Contains(model.input, kind) {
			t.Fatal("preference sent as evidence", kind)
		}
	}
	if d.JSON(c) != before {
		t.Fatal("saved candidate was changed")
	}
}

func TestComparisonWithdrawsOnlyKnownMisuseAndPreservesVerifiedItems(t *testing.T) {
	for _, kind := range []string{"LIMITATION", "ROLE", "CITY_PREFERRED", "CITY_ACCEPTABLE", "JOB_TYPE_PREFERENCE"} {
		for _, status := range []string{"DIRECT", "PARTIAL", "TRANSFERABLE"} {
			t.Run(kind+"/"+status, func(t *testing.T) {
				c := Candidate{Facts: []Fact{{ID: "f", Kind: "IMPLEMENTED", Text: "实现 Go 服务"}, {ID: "bad", Kind: kind, Text: "仅限课程项目"}}}
				reqs := []Requirement{{ID: "a", Category: "REQUIRED", Confidence: 1}, {ID: "b", Category: "REQUIRED", Confidence: 1}}
				output := []Match{{RequirementID: "a", Result: "DIRECT", Explanation: "有具体实现", Evidence: []Citation{{"f", "Go 服务"}}}, {RequirementID: "b", Result: status, Explanation: "原错误推断", Evidence: []Citation{{"f", "Go 服务"}, {"bad", "仅限课程项目"}}}}
				model := &fakeModel{output: map[string]any{"jobs": []any{map[string]any{"job_id": "j", "matches": output}}}}
				got, err := Compare(context.Background(), model, c, []MatchInput{{ID: "j", Requirements: reqs}})
				if err != nil {
					t.Fatal(err)
				}
				matches := got["j"]
				if matches[0].Result != "DIRECT" || matches[0].ReviewNote != "" || matches[1].Result != "NO_EVIDENCE" || len(matches[1].Evidence) != 0 || matches[1].ReviewNote != InvalidAbilityEvidence || strings.Contains(matches[1].Explanation, "原错误推断") || EvidenceReviewCount(matches) != 1 || model.calls != 1 {
					t.Fatal(matches, model.calls)
				}
				if err := ValidateMatches(c, reqs, matches); err != nil {
					t.Fatal(err)
				}
				score, coverage := Score(reqs, matches)
				if score != nil || coverage != 50 {
					t.Fatal("withdrawn proof produced a reliable score", score, coverage)
				}
				in := DecisionInput{Job: d.Job{ID: "j", Title: "后端开发"}, State: "ANALYZED", Result: &Result{Requirements: reqs, Matches: matches, CandidateFacts: c.Facts}}
				plan := BuildPreparation(in, d.Profile{}, decisionNow)
				if plan.EvidenceReviews != 1 {
					t.Fatal(plan)
				}
				found := false
				for _, task := range plan.Tasks {
					if task.SelectedRequirementID == "b" {
						found = true
						if task.Kind != "EVIDENCE" || len(task.Evidence) != 0 || task.Result != "NO_EVIDENCE" {
							t.Fatal(task)
						}
					}
				}
				if !found {
					t.Fatal("missing evidence task")
				}
				report := BuildCompanyComparison("company", "ALL", []DecisionInput{in}, d.Profile{}, decisionNow)
				if report.Recommendation != "READY" || report.Jobs[0].EvidenceReviews != 1 || report.Jobs[0].Score != nil || report.Jobs[0].Priority == nil || report.Jobs[0].Priority.Score != 75 {
					t.Fatal(report)
				}
			})
		}
	}
}

func TestKnownMisuseCannotHideForgedCitationOrExplanation(t *testing.T) {
	c := Candidate{Facts: []Fact{{ID: "limit", Kind: "LIMITATION", Text: "没有生产环境经验"}}}
	reqs := []Requirement{{ID: "r", Category: "REQUIRED"}}
	for _, tt := range []struct {
		reason string
		m      Match
	}{
		{"FACT_UNKNOWN", Match{RequirementID: "r", Result: "DIRECT", Explanation: "错误证明", Evidence: []Citation{{"limit", "没有生产环境经验"}, {"invented", "伪造"}}}},
		{"EXCERPT_NOT_EXACT", Match{RequirementID: "r", Result: "DIRECT", Explanation: "错误证明", Evidence: []Citation{{"limit", "没有生产环境经验"}, {"limit", "生产环境开发"}}}},
		{"EXPLANATION_SENSITIVE", Match{RequirementID: "r", Result: "DIRECT", Explanation: "联系 secret@example.com", Evidence: []Citation{{"limit", "没有生产环境经验"}}}},
	} {
		model := &fakeModel{output: map[string]any{"jobs": []any{map[string]any{"job_id": "j", "matches": []Match{tt.m}}}}}
		_, err := Compare(context.Background(), model, c, []MatchInput{{ID: "j", Requirements: reqs}})
		var validation *ValidationError
		if !errors.As(err, &validation) || validation.Reason != tt.reason || model.calls != 1 {
			t.Fatal(tt.reason, err)
		}
	}
	model := &fakeModel{output: map[string]any{"jobs": []any{map[string]any{"job_id": "j", "matches": []Match{{RequirementID: "r", Result: "NO_EVIDENCE", Explanation: "伪造本地标记", Evidence: []Citation{}, ReviewNote: InvalidAbilityEvidence}}}}}}
	_, err := Compare(context.Background(), model, c, []MatchInput{{ID: "j", Requirements: reqs}})
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Reason != "RESPONSE_SCHEMA" {
		t.Fatal("model forged local annotation", err)
	}
	// A limitation remains useful for an actual contradiction, never a positive.
	model.output = map[string]any{"jobs": []any{map[string]any{"job_id": "j", "matches": []Match{{RequirementID: "r", Result: "MISMATCH", Explanation: "明确缺少所要求的生产经验", Evidence: []Citation{{"limit", "没有生产环境经验"}}}}}}}
	got, err := Compare(context.Background(), model, c, []MatchInput{{ID: "j", Requirements: reqs}})
	if err != nil || got["j"][0].Result != "MISMATCH" || got["j"][0].ReviewNote != "" {
		t.Fatal(got, err)
	}
}

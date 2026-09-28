package matching

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestComparisonValidationDiagnosesWithoutEchoingCandidateOrOutput(t *testing.T) {
	c := Candidate{Facts: []Fact{{ID: "fact", Kind: "IMPLEMENTED", Text: "private-source：用 Go 实现重试"}, {ID: "limit", Kind: "LIMITATION", Text: "无生产流量"}}}
	req := []Requirement{{ID: "r", Category: "REQUIRED"}}
	valid := Match{RequirementID: "r", Result: "PARTIAL", Explanation: "有部分实现", Evidence: []Citation{{ID: "fact", Excerpt: "Go"}}}
	tests := []struct {
		reason  string
		matches []Match
	}{
		{"MATCH_COUNT", nil},
		{"REQUIREMENT_UNKNOWN", []Match{{RequirementID: "private-invented-id"}}},
		{"EXPLANATION_EMPTY", []Match{{RequirementID: "r"}}},
		{"RESULT_UNKNOWN", []Match{{RequirementID: "r", Result: "made-up", Explanation: "判断"}}},
		{"EVIDENCE_REQUIRED", []Match{{RequirementID: "r", Result: "DIRECT", Explanation: "判断"}}},
	}
	for _, tt := range tests {
		err := ValidateMatches(c, req, tt.matches)
		var v *ValidationError
		if !errors.Is(err, ErrInvalid) || !errors.As(err, &v) || v.Reason != tt.reason {
			t.Fatal(tt.reason, err)
		}
		if strings.Contains(err.Error(), "private") {
			t.Fatal("validator echoed private data")
		}
	}
	for _, tt := range []struct {
		reason string
		cite   Citation
	}{{"FACT_UNKNOWN", Citation{"made-up", "private-source"}}, {"EXCERPT_NOT_EXACT", Citation{"fact", "Go开发重试"}}, {"FACT_NOT_ABILITY", Citation{"limit", "无生产流量"}}, {"EXCERPT_LENGTH", Citation{"fact", strings.Repeat("长", 201)}}} {
		m := valid
		m.Evidence = []Citation{tt.cite}
		err := ValidateMatches(c, req, []Match{m})
		var v *ValidationError
		if !errors.As(err, &v) || v.Reason != tt.reason || v.ItemIndex != 1 {
			t.Fatal(tt.reason, err)
		}
	}
}
func TestComparisonResolvesPreferencesBeforeModelCitationValidationButKeepsAbilityChecks(t *testing.T) {
	c := Candidate{Facts: []Fact{{ID: "city", Kind: "CITY_PREFERRED", Text: "Shanghai"}}}
	reqs := []Requirement{{ID: "city-r", Category: "QUALIFICATION", ClaimType: "LOCATION", Value: "上海市"}, {ID: "tech", Category: "REQUIRED"}}
	output := []Match{{RequirementID: "city-r", Result: "MISMATCH", Explanation: "模型错认偏好", Evidence: []Citation{}}, {RequirementID: "tech", Result: "NO_EVIDENCE", Explanation: "暂无技术依据", Evidence: []Citation{}}}
	model := &fakeModel{output: map[string]any{"jobs": []any{map[string]any{"job_id": "job", "matches": output}}}}
	got, err := Compare(context.Background(), model, c, []MatchInput{{ID: "job", Requirements: reqs}})
	if err != nil || got["job"][0].Result != "DIRECT" || got["job"][0].Evidence[0].Excerpt != "Shanghai" || model.calls != 1 {
		t.Fatal(got, err)
	}
	output[1] = Match{RequirementID: "tech", Result: "DIRECT", Explanation: "错认能力", Evidence: []Citation{{ID: "city", Excerpt: "Shanghai"}}}
	got, err = Compare(context.Background(), model, c, []MatchInput{{ID: "job", Requirements: reqs}})
	if err != nil || got["job"][1].Result != "NO_EVIDENCE" || got["job"][1].ReviewNote != InvalidAbilityEvidence || len(got["job"][1].Evidence) != 0 || got["job"][0].Result != "DIRECT" {
		t.Fatal("invalid ability judgment was not withdrawn independently", got, err)
	}
	output[0].RequirementID = "tech"
	output[0].Result = "NO_EVIDENCE"
	_, err = Compare(context.Background(), model, c, []MatchInput{{ID: "job", Requirements: reqs}})
	var v *ValidationError
	if !errors.As(err, &v) || v.Reason != "REQUIREMENT_DUPLICATE" {
		t.Fatal("duplicate guard bypassed", err)
	}
}

func TestComparisonSchemaFailuresAreDistinctFromCitationFailures(t *testing.T) {
	for _, tt := range []struct {
		reason string
		output any
	}{{"RESPONSE_SCHEMA", map[string]any{"jobs": nil}}, {"JOB_COUNT", map[string]any{"jobs": []any{}}}, {"JOB_UNKNOWN", map[string]any{"jobs": []any{map[string]any{"job_id": "private-invented", "matches": []any{}}}}}} {
		m := &fakeModel{output: tt.output}
		_, err := Compare(context.Background(), m, Candidate{}, []MatchInput{{ID: "job", Requirements: []Requirement{}}})
		var v *ValidationError
		if !errors.As(err, &v) || v.Reason != tt.reason || strings.Contains(err.Error(), "private") || m.calls != 1 {
			t.Fatal(tt.reason, err)
		}
	}
}

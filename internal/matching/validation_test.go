package matching

import (
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

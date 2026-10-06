package matching

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

func TestComparisonExcerptsRetainCompleteSourceWithinLiteralBounds(t *testing.T) {
	for _, source := range []string{"", "Go", "使用 Go\t与 Redis\n实现任务队列。", strings.Repeat("界", 199) + "🙂完成。" + strings.Repeat("长句", 500), strings.Repeat("a", 330) + "。\n" + strings.Repeat("b", 1000)} {
		excerpts := factExcerpts(source)
		var joined strings.Builder
		for i, e := range excerpts {
			if e.ID == "" || e.Text == "" || len(e.Text) > maxComparisonExcerpt || !utf8.ValidString(e.Text) || !strings.Contains(source, e.Text) {
				t.Fatalf("invalid source chunk %d: bytes=%d", i, len(e.Text))
			}
			joined.WriteString(e.Text)
		}
		if joined.String() != source || d.JSON(excerpts) != d.JSON(factExcerpts(source)) {
			t.Fatal("source changed or IDs were unstable")
		}
	}
	source := strings.Repeat("a", 330) + "。\n" + strings.Repeat("b", 1000)
	if factExcerpts(source)[0].Text != source[:334] {
		t.Fatal("sentence/line boundary was not preferred")
	}
}

func TestComparisonSelectsOriginalExcerptsWithoutCopyingOrExtraCalls(t *testing.T) {
	long := strings.Repeat("实现失败恢复与任务重试。", 25)
	c := Candidate{Revision: 7, Facts: []Fact{{ID: "impl", Kind: "IMPLEMENTED", Text: long, ProjectName: "任务队列"}, {ID: "go", Kind: "LANGUAGE", Text: "Go"}, {ID: "limit", Kind: "LIMITATION", Text: "没有生产环境经验"}, {ID: "city", Kind: "CITY_PREFERRED", Text: "上海"}}}
	before := d.JSON(c)
	reqs := []Requirement{{ID: "r", Category: "REQUIRED", Confidence: 1}}
	wire := []comparisonMatch{{RequirementID: "r", Result: "PARTIAL", Explanation: "有失败恢复实现，部署范围仍需核实", Evidence: []comparisonCitation{{ID: "impl", ExcerptID: "e2"}, {ID: "go", ExcerptID: "e1"}}}}
	model := &inspectingComparisonModel{fakeModel: fakeModel{output: map[string]any{"jobs": []any{map[string]any{"job_id": "a", "matches": wire}, map[string]any{"job_id": "b", "matches": wire}}}}}
	got, err := Compare(context.Background(), model, c, []MatchInput{{ID: "a", Requirements: reqs}, {ID: "b", Requirements: reqs}})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		m := got[id][0]
		if len(m.Evidence) != 2 || m.Evidence[0].Excerpt != factExcerpts(long)[1].Text || m.Evidence[1].Excerpt != "Go" || m.Result != "PARTIAL" || m.ReviewNote != "" {
			t.Fatal("reference was not bound to its own source", m)
		}
		if err := ValidateMatches(c, reqs, got[id]); err != nil {
			t.Fatal("resolved output cannot pass persistence validation", err)
		}
	}
	var input comparisonRequest
	if err := json.Unmarshal([]byte(model.input), &input); err != nil {
		t.Fatal(err)
	}
	var joined strings.Builder
	for _, e := range input.Candidate.Facts[0].Excerpts {
		joined.WriteString(e.Text)
	}
	if joined.String() != long || len(input.Candidate.Facts) != 2 || len(input.Candidate.Limitations) != 1 || strings.Contains(model.input, "CITY_PREFERRED") || model.calls != 1 || d.JSON(c) != before {
		t.Fatal("source was lost, duplicated, modified or retried")
	}
	if ComparisonBytes(c, []MatchInput{{ID: "a", Requirements: reqs}, {ID: "b", Requirements: reqs}}) != len(model.input) {
		t.Fatal("capacity measurement omitted the indexed wire format")
	}
	// The new quote-selection protocol leaves canonical source identities intact.
	canonical := comparisonCandidate{Facts: []Fact{c.Facts[0], c.Facts[1]}, Limitations: []Fact{c.Facts[2]}}
	if ComparisonCandidateHash(c, ComparisonAbilities) != d.Hash(d.JSON(canonical)) {
		t.Fatal("verified old comparisons would unnecessarily incur another call")
	}
}

func TestComparisonRejectsInvalidExcerptReferencesWithSafePositions(t *testing.T) {
	c := Candidate{Facts: []Fact{{ID: "f", Kind: "IMPLEMENTED", Text: "private source：实现 Go 服务"}, {ID: "limit", Kind: "LIMITATION", Text: "没有生产环境经验"}}}
	reqs := []Requirement{{ID: "good", Category: "REQUIRED"}, {ID: "bad", Category: "REQUIRED"}}
	for _, tc := range []struct {
		name, reason string
		evidence     []comparisonCitation
	}{
		{"unknown fact", "FACT_UNKNOWN", []comparisonCitation{{ID: "private-invented", ExcerptID: "e1"}}},
		{"unknown excerpt", "EXCERPT_ID_UNKNOWN", []comparisonCitation{{ID: "f", ExcerptID: "private-invented"}}},
		{"excerpt from another fact", "EXCERPT_ID_UNKNOWN", []comparisonCitation{{ID: "f", ExcerptID: "e2"}}},
		{"ambiguous mode", "EXCERPT_REFERENCE_CONFLICT", []comparisonCitation{{ID: "f", ExcerptID: "e1", Excerpt: "Go"}}},
		{"empty", "EXCERPT_EMPTY", []comparisonCitation{{ID: "f"}}},
		{"paraphrase", "EXCERPT_NOT_EXACT", []comparisonCitation{{ID: "f", Excerpt: "开发 Go 后端服务"}}},
		{"bad reference beside limitation", "EXCERPT_ID_UNKNOWN", []comparisonCitation{{ID: "limit", ExcerptID: "e1"}, {ID: "f", ExcerptID: "e9"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			matches := []comparisonMatch{{RequirementID: "good", Result: "DIRECT", Explanation: "有效实现", Evidence: []comparisonCitation{{ID: "f", ExcerptID: "e1"}}}, {RequirementID: "bad", Result: "DIRECT", Explanation: "待核对判断", Evidence: tc.evidence}}
			model := &fakeModel{output: map[string]any{"jobs": []any{map[string]any{"job_id": "job", "matches": matches}}}}
			got, err := Compare(context.Background(), model, c, []MatchInput{{ID: "job", Requirements: reqs}})
			var validation *ValidationError
			if got != nil || !errors.As(err, &validation) || validation.Reason != tc.reason || validation.JobIndex != 1 || validation.ItemIndex != 2 || strings.Contains(err.Error(), "private") || model.calls != 1 {
				t.Fatal("invalid reference passed or leaked source", err)
			}
		})
	}
}

func TestComparisonIndexedLimitationsRemainContradictionsNeverAbilityProof(t *testing.T) {
	c := Candidate{Facts: []Fact{{ID: "l", Kind: "LIMITATION", Text: "没有生产环境经验"}}}
	reqs := []Requirement{{ID: "r", Category: "REQUIRED"}}
	for _, status := range []string{"DIRECT", "PARTIAL", "TRANSFERABLE", "MISMATCH"} {
		model := &fakeModel{output: map[string]any{"jobs": []any{map[string]any{"job_id": "job", "matches": []comparisonMatch{{RequirementID: "r", Result: status, Explanation: "对照生产经验", Evidence: []comparisonCitation{{ID: "l", ExcerptID: "e1"}}}}}}}}
		got, err := Compare(context.Background(), model, c, []MatchInput{{ID: "job", Requirements: reqs}})
		if err != nil {
			t.Fatal(err)
		}
		m := got["job"][0]
		if status == "MISMATCH" {
			if m.Result != status || m.Evidence[0].Excerpt != c.Facts[0].Text {
				t.Fatal(m)
			}
		} else if m.Result != "NO_EVIDENCE" || len(m.Evidence) != 0 || m.ReviewNote != InvalidAbilityEvidence {
			t.Fatal("indexed limitation became ability", m)
		}
	}
}

func TestComparisonLegacyWhitespaceRestorationKeepsLiteralValidation(t *testing.T) {
	for _, tc := range []struct {
		name, source, quote, expected, reason string
	}{
		{"exact", "实现 Go 服务", "Go", "Go", ""},
		{"unicode whitespace", "实现 Go\t服务\n与\u00a0Redis", "Go 服务 与 Redis", "Go\t服务\n与\u00a0Redis", ""},
		{"ambiguous", "Go  服务；Go\t服务", "Go 服务", "", "EXCERPT_AMBIGUOUS"},
		{"paraphrase", "实现 Go 服务", "开发 Go 服务", "", "EXCERPT_NOT_EXACT"},
		{"case change", "使用 Go", "使用 go", "", "EXCERPT_NOT_EXACT"},
		{"punctuation change", "Go / Java", "Go、Java", "", "EXCERPT_NOT_EXACT"},
		{"number change", "处理 20 次", "处理 200 次", "", "EXCERPT_NOT_EXACT"},
		{"concatenated facts", "使用 Go。尚未使用 Kafka", "使用 Go Kafka", "", "EXCERPT_NOT_EXACT"},
		{"restored length", "首" + strings.Repeat(" ", 600) + "尾", "首尾", "", "EXCERPT_LENGTH"},
		{"empty whitespace", "Go", " \n\t", "", "EXCERPT_EMPTY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Candidate{Facts: []Fact{{ID: "f", Kind: "IMPLEMENTED", Text: tc.source}}}
			reqs := []Requirement{{ID: "r", Category: "REQUIRED"}}
			m := Match{RequirementID: "r", Result: "PARTIAL", Explanation: "有部分依据", Evidence: []Citation{{ID: "f", Excerpt: tc.quote}}}
			model := &fakeModel{output: map[string]any{"jobs": []any{map[string]any{"job_id": "j", "matches": []Match{m}}}}}
			got, err := Compare(context.Background(), model, c, []MatchInput{{ID: "j", Requirements: reqs}})
			if tc.reason != "" {
				var validation *ValidationError
				if !errors.As(err, &validation) || validation.Reason != tc.reason || validation.JobIndex != 1 || validation.ItemIndex != 1 {
					t.Fatal(err)
				}
				return
			}
			if err != nil || got["j"][0].Evidence[0].Excerpt != tc.expected || ValidateMatches(c, reqs, got["j"]) != nil || model.calls != 1 {
				t.Fatal("quote was not restored to a literal source span", got, err)
			}
			if tc.quote != tc.expected && ValidateMatches(c, reqs, []Match{m}) == nil {
				t.Fatal("storage validation started accepting non-literal quotes")
			}
		})
	}
}

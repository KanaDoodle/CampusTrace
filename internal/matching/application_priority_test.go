package matching

import (
	"strings"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

func TestPriorityUsesAllCoreUnitsAndKeepsUnknownsNeutral(t *testing.T) {
	sparse := decisionFixture("sparse", "后端开发", "DIRECT", "NO_EVIDENCE", "NO_EVIDENCE", "NO_EVIDENCE", "NO_EVIDENCE")
	covered := decisionFixture("covered", "后端开发", "DIRECT", "DIRECT", "DIRECT", "PARTIAL", "NO_EVIDENCE")
	unknown := decisionFixture("unknown", "后端开发", "NO_EVIDENCE")
	report := BuildCompanyComparison("测试公司", "ALL", []DecisionInput{sparse, covered, unknown}, d.Profile{}, decisionNow)
	if len(report.RecommendedIDs) != 1 || report.RecommendedIDs[0] != "covered" {
		t.Fatal(report)
	}
	byID := map[string]CompanyJob{}
	for _, row := range report.Jobs {
		byID[row.Job.ID] = row
	}
	if v := byID["sparse"]; !v.Comparable || v.Score != nil || v.Priority.Score != 60 || v.Priority.Lower != 20 || v.Priority.Upper != 100 {
		t.Fatal(v)
	}
	if byID["covered"].Priority.Score != 85 || byID["unknown"].Priority != nil || byID["unknown"].Comparable {
		t.Fatal(report)
	}
	// More unknown entries move priority toward neutral; they do not behave
	// like explicit negative evidence, or disappear from the denominator.
	low := BuildCompanyComparison("测试公司", "ALL", []DecisionInput{sparse}, d.Profile{}, decisionNow)
	if low.Recommendation != "READY" || !strings.Contains(strings.Join(low.Reasons, ""), "已知资料较少") {
		t.Fatal(low)
	}
}

func TestPriorityPreservesOneOfAndExcludesBonusAndSoftTraits(t *testing.T) {
	in := decisionFixture("job", "后端开发", "DIRECT", "NO_EVIDENCE", "MISMATCH", "MISMATCH")
	for i := 0; i < 2; i++ {
		in.Result.Requirements[i].GroupID = "choice"
	}
	in.Result.Requirements[2].Category = "BONUS"
	in.Result.Requirements[3].Aspect = "SOFT"
	p := ApplicationPriority(ScoreBreakdown(in.Result.Requirements, in.Result.Matches))
	if p == nil || *p != (Priority{100, 100, 100}) {
		t.Fatal(p)
	}
}

func TestExperienceDeclarationKeepsPartialSupportWithoutInventingPractice(t *testing.T) {
	c := Candidate{Facts: []Fact{{ID: "skill", Kind: "SKILL", Text: "RAG"}, {ID: "work", Kind: "IMPLEMENTED", Text: "搭建 RAG 检索流程"}}}
	for _, tc := range []struct{ text, category, status, fact, want, note string }{
		{"有RAG使用经验", "BONUS", "DIRECT", "skill", "PARTIAL", ExperienceUnconfirmed},
		{"有RAG使用经验", "REQUIRED", "PARTIAL", "skill", "PARTIAL", ExperienceUnconfirmed},
		{"有RAG使用经验", "REQUIRED", "TRANSFERABLE", "skill", "TRANSFERABLE", ExperienceUnconfirmed},
		{"了解RAG原理", "REQUIRED", "DIRECT", "skill", "DIRECT", ""},
		{"有RAG使用经验", "REQUIRED", "DIRECT", "work", "DIRECT", ""},
		{"参与RAG开发", "RESPONSIBILITY", "TRANSFERABLE", "skill", "TRANSFERABLE", ""},
		{"有RAG使用经验", "REQUIRED", "MISMATCH", "skill", "MISMATCH", ""},
	} {
		reqs := []Requirement{{ID: "r", Category: tc.category, Text: tc.text, Confidence: 1}}
		matches := []Match{{RequirementID: "r", Result: tc.status, Explanation: "相关依据", Evidence: []Citation{{ID: tc.fact, Excerpt: "RAG"}}}}
		withdrawInvalidAbilityEvidence(c, reqs, matches)
		if matches[0].Result != tc.want || matches[0].ReviewNote != tc.note || len(matches[0].Evidence) != 1 {
			t.Fatal(tc, matches)
		}
		if err := ValidateMatches(c, reqs, matches); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAISplittingPreservesProficiencyContextAndAlternatives(t *testing.T) {
	text := "深入理解Agent原理与架构，包括 Planning、Memory、Tool Use、Reflection等核心机制"
	reqs := []Requirement{{ID: "r", Category: "REQUIRED", Text: text, Excerpt: text, Confidence: 1}}
	got, err := PrepareCachedRequirements(reqs, text)
	if err != nil || len(got) != 4 {
		t.Fatal(got, err)
	}
	for _, r := range got {
		if !strings.HasPrefix(r.Text, "深入理解Agent ") || r.Excerpt != text {
			t.Fatal(r)
		}
	}
	short := []Requirement{{ID: "memory", Category: "REQUIRED", Text: "理解Agent Memory机制", Excerpt: "Memory", Confidence: 1}}
	got, err = PrepareCachedRequirements(short, text)
	if err != nil || got[0].Text != "深入理解Agent Memory机制" || got[0].Excerpt != text {
		t.Fatal(got, err)
	}
	for _, choice := range []string{"熟悉 Planning、Memory、Tool Use 中任一机制", "熟悉 LangGraph、AutoGen 等框架"} {
		got, err := PrepareCachedRequirements([]Requirement{{ID: "choice", Category: "REQUIRED", Text: choice, Excerpt: choice, Confidence: 1}}, choice)
		if err != nil || len(got) != 1 {
			t.Fatal(got, err)
		}
	}
	rag := "了解RAG技术原理，包括文档解析、分块策略、向量化、检索排序"
	got, err = PrepareCachedRequirements([]Requirement{{ID: "rag", Category: "REQUIRED", Text: rag, Excerpt: rag, Confidence: 1}}, rag)
	if err != nil || len(got) != 4 {
		t.Fatal(got, err)
	}
}

func TestChatContextRepairDoesNotBlockImportOrCopyJudgments(t *testing.T) {
	text := "深入理解Agent原理，包括 Planning、Memory、Tool Use 等核心机制"
	reqs := []Requirement{
		{ID: "parent", Category: "REQUIRED", Text: "深入理解Agent原理", Excerpt: text, Confidence: 1},
		{ID: "planning", Category: "REQUIRED", Text: "理解Agent Planning机制", Excerpt: "Planning", Confidence: 1},
		{ID: "memory", Category: "REQUIRED", Text: "理解Agent Memory机制", Excerpt: "Memory", Confidence: 1},
		{ID: "tools", Category: "REQUIRED", Text: "理解Agent Tool Use机制", Excerpt: "Tool Use", Confidence: 1},
	}
	matches := []Match{
		{RequirementID: "parent", Result: "PARTIAL", Explanation: "部分理解", Evidence: []Citation{{ID: "work", Excerpt: "工具调用"}}},
		{RequirementID: "planning", Result: "NO_EVIDENCE", Explanation: "尚无规划依据", Evidence: []Citation{}},
		{RequirementID: "memory", Result: "NO_EVIDENCE", Explanation: "尚无记忆依据", Evidence: []Citation{}},
		{RequirementID: "tools", Result: "DIRECT", Explanation: "实现工具调用", Evidence: []Citation{{ID: "work", Excerpt: "工具调用"}}},
	}
	c := Candidate{Facts: []Fact{{ID: "work", Kind: "IMPLEMENTED", Text: "实现工具调用"}}}
	got, err := ImportChatJob(ChatJob{Requirements: reqs, Matches: matches}, text, d.Job{ID: "job"}, d.Profile{}, c, decisionNow)
	if err != nil || len(got.Requirements) != 3 || len(got.Matches) != 3 {
		t.Fatal(got, err)
	}
	if got.Matches[2].Result != "PARTIAL" || got.Matches[2].ReviewNote != ContextUnconfirmed || got.Matches[0].Result != "NO_EVIDENCE" {
		t.Fatal(got.Matches)
	}
	for _, r := range got.Requirements {
		if r.Excerpt != text || !strings.HasPrefix(r.Text, "深入理解") {
			t.Fatal(r)
		}
	}
	// A partial component list is not enough to remove the parent condition.
	kept := withoutCoveredUmbrellas(restoreRequirementContext(append([]Requirement{}, reqs[:3]...), text))
	if len(kept) != 3 {
		t.Fatal("removed an incompletely covered umbrella", kept)
	}
	// The restored sentence may include a numbered recruiting-list prefix.
	kept = withoutCoveredUmbrellas(restoreRequirementContext(append([]Requirement{}, reqs...), "3. "+text))
	if len(kept) != 3 {
		t.Fatal("source numbering prevented umbrella deduplication", kept)
	}
}

package matching

import (
	"strings"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

func TestCandidateProjectContextIsRedactedAndInvalidatesOnRename(t *testing.T) {
	p := d.Profile{PreferredCities: []string{"Shanghai"}, AcceptableCities: []string{"杭州"}, PreferredTypes: []string{"FULL_TIME"}}
	facts := []d.ProjectFact{{ID: "f", ProjectID: "p", Kind: "IMPLEMENTED", Verified: true, Claim: "使用 MySQL 行锁 / SKIP LOCKED 处理任务"}, {ID: "draft", ProjectID: "p", Kind: "IMPLEMENTED", Claim: "构建 Agent"}}
	projects := []d.Project{{ID: "p", Name: "张同学的项目 contact@example.com"}}
	c, err := CandidateWithProjects(p, facts, projects, "张同学")
	if err != nil {
		t.Fatal(err)
	}
	raw := d.JSON(c)
	for _, absent := range []string{"张同学", "contact@example.com", "构建 Agent"} {
		if strings.Contains(raw, absent) {
			t.Fatal("unreviewed or identifying content sent", raw)
		}
	}
	if !strings.Contains(raw, "CITY_PREFERRED") || !strings.Contains(raw, "CITY_ACCEPTABLE") || !strings.Contains(raw, "JOB_TYPE_PREFERENCE") || !strings.Contains(raw, "project_name") {
		t.Fatal(raw)
	}
	projects[0].Name = "任务调度平台"
	next, err := CandidateWithProjects(p, facts, projects, "张同学")
	if err != nil || next.Hash() == c.Hash() {
		t.Fatal("project context edit did not invalidate comparison", err)
	}
}

func TestMatchingKeepsCompleteNarrativeSeparateFromVerifiedFacts(t *testing.T) {
	claim := "使用 Redis Streams 实现异步任务处理，\n通过消费者组和失败重试提高可靠性。"
	projects := []d.Project{{ID: "p", Name: "任务队列", Description: "未选择的简介", Bullets: []string{"计划实现 Kafka", "未选择的完整经历"}}}
	facts := []d.ProjectFact{{ID: "f", ProjectID: "p", Kind: "IMPLEMENTED", Claim: claim, Reference: claim, Verified: true}}
	candidate, err := CandidateWithProjects(d.Profile{}, facts, projects, "")
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Facts[len(candidate.Facts)-1].Text != claim {
		t.Fatal("matching lost the mechanism and result", candidate)
	}
	if len(candidate.Projects) != 1 || candidate.Projects[0].Description != projects[0].Description || candidate.Projects[0].Bullets[0].Text != "计划实现 Kafka" {
		t.Fatal("complete reviewed narrative was lost", candidate)
	}
	for _, f := range candidate.Facts {
		if strings.Contains(f.Text, "Kafka") {
			t.Fatal("project context was promoted into a verified fact")
		}
	}

}

func TestCoreScoreIndependentOfBonusesDutiesAndSoftTraits(t *testing.T) {
	reqs := []Requirement{{ID: "core", Category: "REQUIRED", Confidence: 1}, {ID: "soft", Category: "REQUIRED", Aspect: "SOFT", Confidence: 1}, {ID: "bonus", Category: "BONUS", Confidence: 1}, {ID: "duty", Category: "RESPONSIBILITY", Confidence: 1}}
	matches := []Match{{RequirementID: "core", Result: "PARTIAL"}, {RequirementID: "soft", Result: "NO_EVIDENCE"}, {RequirementID: "bonus", Result: "NO_EVIDENCE"}, {RequirementID: "duty", Result: "NO_EVIDENCE"}}
	score, coverage := Score(reqs, matches)
	if score == nil || *score != 50 || coverage != 100 {
		t.Fatal(score, coverage)
	}
	sections := ScoreBreakdown(reqs, matches)
	if sections[0].Total != 1 || sections[1].Missing != 1 || sections[2].Missing != 1 || sections[3].Missing != 1 || sections[3].Score != nil {
		t.Fatal(sections)
	}
	reqs = append(reqs, Requirement{ID: "unknown", Category: "REQUIRED", Confidence: 1})
	score, coverage = Score(reqs, matches)
	if score != nil || coverage != 50 {
		t.Fatal("unknown core requirement did not suppress score", score, coverage)
	}
}

func TestSelectableDirectionCountsOnceAndUnresolvedAlternativesStayUnknown(t *testing.T) {
	reqs := []Requirement{{ID: "a", Category: "REQUIRED", GroupID: "direction", Confidence: 1}, {ID: "b", Category: "REQUIRED", GroupID: "direction", Confidence: 1}, {ID: "c", Category: "REQUIRED", GroupID: "direction", Confidence: 1}}
	for _, tt := range []struct {
		a, b, c string
		known   int
		score   *float64
	}{{"DIRECT", "NO_EVIDENCE", "NO_EVIDENCE", 1, floatValue(100)}, {"MISMATCH", "NO_EVIDENCE", "MISMATCH", 0, nil}, {"MISMATCH", "MISMATCH", "MISMATCH", 1, floatValue(0)}, {"PARTIAL", "TRANSFERABLE", "NO_EVIDENCE", 1, floatValue(50)}} {
		s := ScoreBreakdown(reqs, []Match{{RequirementID: "a", Result: tt.a}, {RequirementID: "b", Result: tt.b}, {RequirementID: "c", Result: tt.c}})[0]
		if s.Total != 1 || s.Known != tt.known || (s.Score == nil) != (tt.score == nil) || (s.Score != nil && *s.Score != *tt.score) {
			t.Fatal(tt, s)
		}
	}
	reqs[0].Confidence = .7
	if s := ScoreBreakdown(reqs, []Match{{RequirementID: "a", Result: "DIRECT"}})[0]; s.Known != 0 {
		t.Fatal("low-confidence group member counted", s)
	}
}
func floatValue(v float64) *float64 { return &v }

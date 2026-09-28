package matching

import (
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"strings"
	"testing"
	"time"
)

var decisionNow = time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)

func decisionFixture(id, title string, statuses ...string) DecisionInput {
	reqs := []Requirement{}
	matches := []Match{}
	for i, status := range statuses {
		rid := string(rune('a' + i))
		reqs = append(reqs, Requirement{ID: rid, Category: "REQUIRED", Text: "技术要求 " + rid, Excerpt: "原文 " + rid, Confidence: 1})
		matches = append(matches, Match{RequirementID: rid, Result: status, Explanation: "已保存的逐项判断", Evidence: []Citation{{ID: "fact", Excerpt: "Go 任务队列"}}})
	}
	return DecisionInput{Job: d.Job{ID: id, Company: "测试公司", Title: title, CurrentStatus: "UNKNOWN", JobType: "FULL_TIME", Locations: []string{"Shanghai"}}, State: "ANALYZED", Result: &Result{InputKey: "current", Requirements: reqs, Matches: matches, CandidateFacts: []Fact{{ID: "fact", Kind: "IMPLEMENTED", ProjectName: "任务调度", Text: "Go 任务队列"}}, AnalyzedAt: decisionNow}}
}
func TestDecisionNeverUsesStaleOrUnscoredResults(t *testing.T) {
	stale := decisionFixture("stale", "后端开发", "DIRECT")
	stale.State = "STALE"
	low := decisionFixture("low", "后端开发", "DIRECT", "NO_EVIDENCE", "NO_EVIDENCE")
	pending := DecisionInput{Job: d.Job{ID: "pending", Title: "后端开发"}, State: "BASIC"}
	report := BuildCompanyComparison("测试公司", "ALL", []DecisionInput{stale, low, pending}, d.Profile{}, decisionNow)
	if report.Recommendation != "NONE" || len(report.RecommendedIDs) != 0 || report.Stale != 1 || report.Pending != 1 {
		t.Fatal(report)
	}
	for _, row := range report.Jobs {
		if row.Score != nil {
			t.Fatal("unreliable score exposed", row)
		}
	}
	for _, in := range []DecisionInput{stale, pending} {
		plan := BuildPreparation(in, d.Profile{}, decisionNow)
		if len(plan.Tasks) != 0 || plan.InputKey != "" {
			t.Fatal("historical facts became current tasks", plan)
		}
	}
}
func TestDecisionRecommendationKeepsTiesAndIncompleteScope(t *testing.T) {
	a, b := decisionFixture("a", "后端开发", "DIRECT"), decisionFixture("b", "服务端开发", "DIRECT")
	report := BuildCompanyComparison("测试公司", "SELECTED", []DecisionInput{b, a}, d.Profile{TargetRoles: []string{"后端开发"}}, decisionNow)
	if report.Recommendation != "TIED" || len(report.RecommendedIDs) != 2 {
		t.Fatal(report)
	}
	partial := BuildCompanyComparison("测试公司", "ALL", []DecisionInput{a, {Job: d.Job{ID: "new"}, State: "BASIC"}}, d.Profile{}, decisionNow)
	if partial.Recommendation != "PARTIAL" || partial.Pending != 1 || !strings.Contains(strings.Join(partial.Reasons, ""), "不代表整家公司") {
		t.Fatal(partial)
	}
}
func TestDecisionHonorsDirectionAndDoesNotRecommendClosedIgnoredOrIneligible(t *testing.T) {
	backend := decisionFixture("backend", "服务端开发", "PARTIAL")
	algorithm := decisionFixture("algorithm", "推荐算法工程师", "DIRECT")
	closed := decisionFixture("closed", "后端开发", "DIRECT")
	closed.Job.CurrentStatus = "CLOSED"
	ignored := decisionFixture("ignored", "后端开发", "DIRECT")
	ignored.ExcludedReason = "你已忽略这个岗位"
	ineligible := decisionFixture("degree", "后端开发", "DIRECT")
	ineligible.Result.Requirements = append(ineligible.Result.Requirements, Requirement{ID: "degree", Category: "QUALIFICATION", ClaimType: "EDUCATION_REQUIREMENT", Value: "PHD", Text: "博士", Excerpt: "博士", Confidence: 1})
	report := BuildCompanyComparison("测试公司", "ALL", []DecisionInput{algorithm, backend, closed, ignored, ineligible}, d.Profile{TargetRoles: []string{"后端开发"}, Degree: "BACHELOR"}, decisionNow)
	if len(report.RecommendedIDs) != 1 || report.RecommendedIDs[0] != "backend" {
		t.Fatal(report)
	}
	for _, row := range report.Jobs {
		if row.Job.ID == "closed" || row.Job.ID == "ignored" || row.Job.ID == "degree" {
			if row.Comparable || row.Recommended {
				t.Fatal("excluded job recommended", row)
			}
		}
	}
}
func TestDecisionUsesCityAliasesOnlyAsTieBreakerAndRecomputesScores(t *testing.T) {
	a, b := decisionFixture("a", "后端开发", "DIRECT"), decisionFixture("b", "后端开发", "DIRECT")
	a.Job.Locations = []string{"北京"}
	b.Job.Locations = []string{"上海市"}
	fabricated := 99.0
	a.Result.Score = &fabricated
	report := BuildCompanyComparison("测试公司", "ALL", []DecisionInput{a, b}, d.Profile{PreferredCities: []string{"Shanghai"}}, decisionNow)
	if report.RecommendedIDs[0] != "b" || *report.Jobs[0].Score != 100 || report.Jobs[0].City != "包含首选城市" {
		t.Fatal(report)
	}
}
func TestPreparationAlternativeUnitMatchesScoreAndKeepsOnlySelectedEvidence(t *testing.T) {
	in := decisionFixture("id", "后端开发", "NO_EVIDENCE", "DIRECT")
	for i := range in.Result.Requirements {
		in.Result.Requirements[i].GroupID = "either"
		in.Result.Requirements[i].GroupExcerpt = "任意一个方向"
	}
	in.Result.Matches[0].Evidence = nil
	plan := BuildPreparation(in, d.Profile{}, decisionNow)
	technical := []PreparationTask{}
	for _, task := range plan.Tasks {
		if task.Category == "REQUIRED" {
			technical = append(technical, task)
		}
	}
	if len(technical) != 1 || technical[0].Result != "DIRECT" || technical[0].SelectedRequirementID != "b" || len(technical[0].Requirements) != 2 || len(technical[0].Evidence) != 1 || *plan.Score != 100 || plan.Coverage != 100 {
		t.Fatal(plan)
	}
	if !strings.Contains(technical[0].Action, "无需把所有方向同时补齐") {
		t.Fatal(technical[0])
	}
}
func TestPreparationSeparatesEvidenceAbsenceAbilityGapsAndSoftOrBonusTasks(t *testing.T) {
	in := decisionFixture("id", "后端开发", "NO_EVIDENCE", "MISMATCH", "TRANSFERABLE", "DIRECT")
	in.Result.Requirements[2].Category = "BONUS"
	in.Result.Requirements[3].Aspect = "SOFT"
	plan := BuildPreparation(in, d.Profile{}, decisionNow)
	byKind := map[string]PreparationTask{}
	for _, task := range plan.Tasks {
		if task.Category != "QUALIFICATION" {
			byKind[task.Kind] = task
		}
	}
	if !strings.Contains(byKind["EVIDENCE"].Action, "不等于不会") || byKind["LEARN"].Priority != 1 || byKind["DEEPEN"].Category != "BONUS" || byKind["DEEPEN"].Priority != 3 || byKind["REHEARSE"].Category != "SOFT" {
		t.Fatal(plan)
	}
	for _, task := range plan.Tasks {
		for _, e := range task.Evidence {
			if e.ProjectName != "任务调度" || e.ID != "fact" || e.Excerpt != "Go 任务队列" {
				t.Fatal("invented project evidence", e)
			}
		}
	}
}

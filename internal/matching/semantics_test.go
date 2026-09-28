package matching

import (
	"context"
	"strings"
	"testing"
	"time"

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

func TestExtractionKeepsPreferencesSoftTraitsAndSelectableDirectionsSeparate(t *testing.T) {
	text := "本科及以上学历，软件工程专业优先。\n责任心强，沟通协作顺畅。\n你将参与一个或多个方向：\n1、数据安全方向：建设数据安全平台；\n2、AI安全方向：建设安全护栏；\n3、以工程化思路建设上述能力。\n任职资格：熟悉 Go。"
	items := []Requirement{
		{Category: "QUALIFICATION", Text: "本科及以上学历", Excerpt: "本科及以上学历，软件工程专业优先。", ClaimType: "EDUCATION_REQUIREMENT", Value: "BACHELOR", Confidence: 1},
		{Category: "QUALIFICATION", Text: "软件工程相关专业", Excerpt: "软件工程专业优先", ClaimType: "MAJOR_REQUIREMENT", Value: "软件工程", Confidence: 1},
		{Category: "REQUIRED", Text: "责任心强，沟通协作顺畅", Excerpt: "责任心强，沟通协作顺畅", Confidence: 1},
		{Category: "RESPONSIBILITY", Text: "建设数据安全平台", Excerpt: "建设数据安全平台", Confidence: 1},
		{Category: "RESPONSIBILITY", Text: "建设安全护栏", Excerpt: "建设安全护栏", Confidence: 1},
		{Category: "RESPONSIBILITY", Text: "以工程化思路建设上述能力", Excerpt: "以工程化思路建设上述能力", Confidence: 1},
	}
	m := &fakeModel{output: map[string]any{"jobs": []any{map[string]any{"job_id": "j", "requirements": items}}}}
	got, err := Extract(context.Background(), m, []JobText{{ID: "j", Text: text}})
	if err != nil {
		t.Fatal(err)
	}
	q := got["j"].Items
	if q[0].Category != "QUALIFICATION" || q[1].Category != "BONUS" || q[1].ClaimType != "" || q[1].Value != "" || q[2].Aspect != "SOFT" {
		t.Fatal(q)
	}
	if q[3].GroupID == "" || q[3].GroupID != q[4].GroupID || q[5].GroupID != "" {
		t.Fatal("selectable directions or shared duty lost", q)
	}
	e := Qualification(d.Job{JobType: "FULL_TIME"}, d.Profile{Degree: "MASTER", Majors: []string{"数学"}}, q, time.Now())
	for _, row := range e.Results {
		if row.Rule == "MAJOR_REQUIREMENT" && row.Result != "NOT_APPLICABLE" {
			t.Fatal("preferred major became gate", row)
		}
	}
	m.output = map[string]any{"jobs": []any{map[string]any{"job_id": "j", "truncated": true, "requirements": items}}}
	if _, err := Extract(context.Background(), m, []JobText{{ID: "j", Text: text}}); err != ErrCapacity {
		t.Fatal("silently accepted truncated extraction", err)
	}
}

func TestInvalidSelectableGroupsCannotHideMandatoryRequirements(t *testing.T) {
	r := Requirement{Category: "REQUIRED", Text: "掌握 Go", Excerpt: "掌握 Go", Confidence: 1, GroupID: "languages", GroupExcerpt: "必须掌握 Go 和 Java"}
	if ValidateRequirement(r, "必须掌握 Go 和 Java") != ErrInvalid {
		t.Fatal("AND group accepted as ANY")
	}
	if validateGroups([]Requirement{{GroupID: "a", Category: "REQUIRED"}, {GroupID: "a", Category: "BONUS"}}) != ErrInvalid {
		t.Fatal("mixed group accepted")
	}
	priority := normalizeRequirements([]Requirement{{Category: "REQUIRED", Text: "实现优先级调度", Excerpt: "实现优先级调度"}}, "实现优先级调度")[0]
	if priority.Category != "REQUIRED" {
		t.Fatal("priority scheduling was classified as a bonus", priority)
	}
	bad := Requirement{Category: "BONUS", Text: "有大模型及 Agent 相关实际开发经验优先", Excerpt: "有实际开发经验优先", Confidence: .9}
	if ValidateRequirement(bad, "了解大模型及 Agent 应用开发，有实际开发经验优先") != ErrInvalid {
		t.Fatal("subjectless excerpt was accepted as AI evidence")
	}
}

func TestComparisonUsesSavedCityAndTypePreferencesWithoutInventingAbilities(t *testing.T) {
	c, err := CandidateFrom(d.Profile{PreferredCities: []string{"Shanghai"}, AcceptableCities: []string{"Hangzhou"}, PreferredTypes: []string{"FULL_TIME"}}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	reqs := []Requirement{{ID: "city", Category: "QUALIFICATION", ClaimType: "LOCATION", Value: "北京市|上海市|杭州市"}, {ID: "type", Category: "QUALIFICATION", ClaimType: "JOB_TYPE", Value: "FULL_TIME"}}
	matches := []Match{{RequirementID: "city", Result: "NO_EVIDENCE", Explanation: "未填写城市", Evidence: []Citation{}}, {RequirementID: "type", Result: "NO_EVIDENCE", Explanation: "未填写类型", Evidence: []Citation{}}}
	m := &fakeModel{output: map[string]any{"jobs": []any{map[string]any{"job_id": "j", "matches": matches}}}}
	if err := ValidateMatches(c, reqs, matches); err != nil {
		t.Fatal("before preference correction", err, matches)
	}
	corrected := comparePreferences(c, reqs, append([]Match{}, matches...))
	if err := ValidateMatches(c, reqs, corrected); err != nil {
		t.Fatal("after preference correction", err, c, corrected)
	}
	got, err := Compare(context.Background(), m, c, []MatchInput{{ID: "j", Requirements: reqs}})
	if err != nil {
		t.Fatal(err)
	}
	if got["j"][0].Result != "DIRECT" || got["j"][1].Result != "DIRECT" || got["j"][0].Evidence[0].Excerpt != "Shanghai" {
		t.Fatal(got)
	}
	if ValidateMatches(c, []Requirement{{ID: "tech", Category: "REQUIRED"}}, []Match{{RequirementID: "tech", Result: "DIRECT", Explanation: "城市不是能力", Evidence: got["j"][0].Evidence}}) == nil {
		t.Fatal("preference proved technical ability")
	}
	missing := comparePreferences(Candidate{}, reqs, matches)
	if missing[0].Result != "NO_EVIDENCE" {
		t.Fatal(missing)
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

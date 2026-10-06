package matching

import (
	"context"
	"strings"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

func qualificationRow(e d.Eligibility, typ string) d.RuleResult {
	for _, row := range e.Results {
		if row.Rule == typ {
			return row
		}
	}
	return d.RuleResult{}
}

func TestGraduationWindowsDoNotTurnYearOverlapIntoEligibility(t *testing.T) {
	for _, tc := range []struct {
		text  string
		month int
		want  string
	}{
		{"2026年10月1日至2027年9月30日期间毕业", 0, "UNKNOWN"},
		{"2026年10月1日至2027年9月30日期间毕业", 6, "PASS"},
		{"2026年10月1日至2027年9月30日期间毕业", 10, "FAIL"},
		{"2026年10月1日至2027年9月15日期间毕业", 9, "UNKNOWN"},
		{"2026年10月至2027年9月期间毕业", 9, "PASS"},
		{"2026年10月1日至2027年9月30日期间毕业；2026年9月1日至2027年8月1日期间毕业", 6, "UNKNOWN"},
		{"2026年10月1日至2027年2月30日期间毕业", 6, "UNKNOWN"},
	} {
		r := Requirement{ID: "grad", Category: "QUALIFICATION", Text: tc.text, Excerpt: tc.text, ClaimType: "GRADUATION_REQUIREMENT", Value: "2026-2027", Confidence: 1}
		p := d.Profile{GraduationYear: 2027, GraduationMonth: tc.month, Degree: "MASTER"}
		rows := RepairQualifications([]Requirement{r}, tc.text)
		if err := ValidateRequirement(rows[0], tc.text); err != nil {
			t.Fatal(tc.text, err)
		}
		got := qualificationRow(Qualification(d.Job{JobType: "FULL_TIME"}, p, rows, time.Time{}), "GRADUATION_REQUIREMENT")
		if got.Result != tc.want || !strings.Contains(got.Requirement, " 至 ") {
			t.Fatal(tc, got)
		}
	}
	// A recruiting/application deadline is not a graduation window.
	if graduationWindow("网申开始日期：2026-09-01\n网申截止日期：2027-08-01") != nil {
		t.Fatal("application period became graduation qualification")
	}
	// A model's shortened cohort value must not reject a date within the
	// literal range's other boundary year.
	text := "2026年10月1日至2027年9月30日期间毕业"
	r := Requirement{ID: "grad", Category: "QUALIFICATION", Text: text, Excerpt: text, ClaimType: "GRADUATION_REQUIREMENT", Value: "2027", Confidence: 1}
	got := qualificationRow(Qualification(d.Job{}, d.Profile{GraduationYear: 2026, GraduationMonth: 12}, []Requirement{r}, time.Time{}), "GRADUATION_REQUIREMENT")
	if got.Result != "PASS" {
		t.Fatal("cohort value overrode exact dates", got)
	}
}

func TestSavedResultsRepairLocalQualificationsWithoutChangingAbilityOrHistory(t *testing.T) {
	source := "2027届应届毕业生，本科及以上学历，计算机相关专业\n毕业范围开始日期：2026-09-01 00:00:00\n毕业范围结束日期：2027-08-01 00:00:00\n掌握 Go"
	p := d.Profile{Educations: []d.Education{{ID: "m", Degree: "MASTER", Majors: []string{"软件工程"}, GraduationYear: 2027, Status: "ENROLLED"}, {ID: "b", Degree: "BACHELOR", Majors: []string{"工商管理"}, GraduationYear: 2023, Status: "GRADUATED"}}, PrimaryEducationID: "m"}
	c, err := CandidateFrom(p, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	reqs := []Requirement{
		{ID: "grad", Category: "QUALIFICATION", Text: "2027届应届毕业生，本科及以上学历", Excerpt: "2027届应届毕业生，本科及以上学历", ClaimType: "GRADUATION_REQUIREMENT", Value: "2027", Confidence: 1},
		{ID: "major", Category: "QUALIFICATION", Text: "计算机相关专业", Excerpt: "计算机相关专业", ClaimType: "MAJOR_REQUIREMENT", Value: "计算机", Confidence: 1},
		{ID: "go", Category: "REQUIRED", Text: "掌握 Go", Excerpt: "掌握 Go", Confidence: 1},
	}
	c.Facts = append(c.Facts, Fact{ID: "go", Kind: "LANGUAGE", Text: "Go"})
	proof := []Match{{RequirementID: "major", Result: "DIRECT", Explanation: "专业相关", Evidence: []Citation{{ID: "education-m-major-0", Excerpt: "硕士专业：软件工程"}}}, {RequirementID: "go", Result: "DIRECT", Explanation: "已记录 Go", Evidence: []Citation{{ID: "go", Excerpt: "Go"}}}}
	matches, err := AssembleMatches(p, c, reqs, proof)
	if err != nil {
		t.Fatal(err)
	}
	score, coverage := Score(reqs, matches)
	old := Result{Requirements: reqs, Matches: matches, ComparisonScope: ComparisonFull, Score: score, Coverage: coverage, AnalyzedAt: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)}
	before := d.JSON(old)
	got, err := RefreshResult(old, d.Job{JobType: "FULL_TIME"}, p, c, "same", c.Hash(), time.Now(), source)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Requirements) != 4 || got.AnalyzedAt != old.AnalyzedAt || *got.Score != *old.Score || d.JSON(old) != before {
		t.Fatal("local repair rewrote history or paid ability", got)
	}
	for typ, want := range map[string]string{"EDUCATION_REQUIREMENT": "PASS", "MAJOR_REQUIREMENT": "PASS", "GRADUATION_REQUIREMENT": "UNKNOWN"} {
		if row := qualificationRow(got.Qualifications, typ); row.Result != want {
			t.Fatal(typ, row)
		}
	}
	p.Educations[0].GraduationMonth = 6
	if row := qualificationRow(Qualification(d.Job{}, p, got.Requirements, time.Time{}), "GRADUATION_REQUIREMENT"); row.Result != "PASS" {
		t.Fatal(row)
	}
	// Graduate software engineering cannot satisfy a bachelor-bound major.
	reqs[1].Excerpt, reqs[1].Text = "本科专业须为计算机相关专业", "本科专业须为计算机相关专业"
	gotMatches, err := AssembleMatches(p, c, reqs, proof)
	if err != nil || gotMatches[1].Result != "NO_EVIDENCE" {
		t.Fatal(gotMatches, err)
	}
}

func TestQualificationRepairsRespectPreferencesAndUnsupportedConditions(t *testing.T) {
	for _, text := range []string{"本科及以上学历优先", "博士学历", "学历不限"} {
		r := Requirement{ID: "q", Category: "QUALIFICATION", Text: text, Excerpt: text, Confidence: 1}
		if len(RepairQualifications([]Requirement{r}, text)) != 1 {
			t.Fatal("invented mandatory minimum", text)
		}
	}
	r := Requirement{ID: "q", Category: "QUALIFICATION", ClaimType: "EDUCATION_REQUIREMENT", Value: "BACHELOR", Text: "统招本科及以上学历", Excerpt: "统招本科及以上学历", Confidence: 1}
	e := Qualification(d.Job{JobType: "FULL_TIME"}, d.Profile{GraduationYear: 2027, Degree: "MASTER"}, []Requirement{r}, time.Time{})
	if qualificationRow(e, "OTHER_QUALIFICATION").Result != "UNKNOWN" || e.Status == "ELIGIBLE" {
		t.Fatal(e)
	}
}

func TestExtractionSeparatesFoundationAndSoftTraitsButPreservesLanguageChoices(t *testing.T) {
	text := "热爱技术，熟练掌握 Go\n熟悉操作系统、网络(TCP/IP)、数据结构与常见算法等基础原理\n了解TCP/IP、UDP、FTP、HTTP、HTTPS的基本原理及应用场景\n熟练掌握Go/Java/C++至少一门语言\n对 AI 技术保持好奇心，主动学习并掌握新的AI知识和功能"
	items := []Requirement{}
	for _, line := range strings.Split(text, "\n") {
		items = append(items, Requirement{Category: "REQUIRED", Text: line, Excerpt: line, Aspect: "TECHNICAL", Confidence: 1})
	}
	model := &fakeModel{output: map[string]any{"jobs": []any{map[string]any{"job_id": "j", "requirements": items}}}}
	got, err := Extract(context.Background(), model, []JobText{{ID: "j", Text: text}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got["j"].Items) != 13 {
		t.Fatal(got["j"].Items)
	}
	soft, language := 0, 0
	matches := []Match{}
	for _, q := range got["j"].Items {
		if q.Aspect == "SOFT" {
			soft++
		}
		if q.Excerpt == items[3].Excerpt {
			language++
		}
		matches = append(matches, Match{RequirementID: q.ID, Result: "NO_EVIDENCE"})
	}
	if soft != 2 || language != 1 {
		t.Fatal(soft, language)
	}
	sections := ScoreBreakdown(got["j"].Items, matches)
	if sections[0].Total != 11 || sections[3].Total != 2 {
		t.Fatal(sections)
	}
	// All-noun alternatives remain one requirement, not multiple hard gaps.
	choices := Requirement{ID: "choice", Category: "REQUIRED", Text: "熟悉操作系统、网络、算法中任一方向", Excerpt: "熟悉操作系统、网络、算法中任一方向"}
	if len(splitTestableRequirements([]Requirement{choices})) != 1 {
		t.Fatal("split alternatives")
	}
}

func TestSoftScoreGuardAndCachedSplitCapacity(t *testing.T) {
	engineering := Requirement{Category: "REQUIRED", Text: "具备清晰的逻辑思维和工程化排查思路，能借助日志、监控、trace等手段深入问题本质"}
	if softOnly(engineering) {
		t.Fatal("specific diagnostic methods became a soft-only requirement")
	}
	for _, trait := range []string{"热爱技术", "对技术有好奇心", "符合公司价值观", "主动学习并掌握新的AI知识", "具有优秀的逻辑思维能力"} {
		reqs := []Requirement{{ID: "go", Category: "REQUIRED", Text: "熟悉 Go", Confidence: 1}, {ID: "soft", Category: "REQUIRED", Aspect: "TECHNICAL", Text: trait, Confidence: 1}}
		for _, status := range []string{"NO_EVIDENCE", "DIRECT", "MISMATCH"} {
			score, coverage := Score(reqs, []Match{{RequirementID: "go", Result: "DIRECT"}, {RequirementID: "soft", Result: status}})
			if score == nil || *score != 100 || coverage != 100 {
				t.Fatal(trait, status, score, coverage)
			}
		}
	}
	text := "熟悉操作系统、网络、数据结构与算法"
	reqs := []Requirement{{ID: "base", Category: "REQUIRED", Text: text, Excerpt: text, Confidence: 1}}
	got, err := PrepareCachedRequirements(reqs, text)
	if err != nil || len(got) != 4 || got[0].ID == got[1].ID || reqs[0].Text != text {
		t.Fatal(got, err)
	}
	for i := 0; i < MaxRequirements; i++ {
		reqs = append(reqs, Requirement{ID: strings.Repeat("x", i+1), Category: "REQUIRED", Text: "熟悉 Go", Excerpt: "Go", Confidence: 1})
	}
	if _, err := PrepareCachedRequirements(reqs, text+" Go"); err != ErrCapacity {
		t.Fatal("silently trimmed split requirements", err)
	}
}

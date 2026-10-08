package matching

import (
	"errors"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

func headingChatFixture() (ChatJob, string, d.Profile, Candidate) {
	p := d.Profile{Languages: []string{"Go"}}
	c, _ := CandidateFrom(p, nil, "")
	return ChatJob{Requirements: []Requirement{
		{ID: "header", Category: "QUALIFICATION", Aspect: "TECHNICAL", Text: "专业能力：", Excerpt: "专业能力：", ClaimType: "MAJOR_REQUIREMENT", Confidence: .95},
		{ID: "go", Category: "REQUIRED", Text: "熟悉 Go", Excerpt: "熟悉 Go", Confidence: 1},
	}, Matches: []Match{
		{RequirementID: "header", Result: "NO_EVIDENCE", Explanation: "分段标题，不是能力条件", Evidence: []Citation{}},
		{RequirementID: "go", Result: "DIRECT", Explanation: "已填写 Go", Evidence: []Citation{{ID: "language-0", Excerpt: "Go"}}},
	}}, "专业能力：\n熟悉 Go", p, c
}

func TestChatIgnoresStandaloneHeadingWithoutChangingRealJudgment(t *testing.T) {
	in, text, p, c := headingChatFixture()
	got, err := ImportChatJob(in, text, d.Job{ID: "job"}, p, c, time.Now())
	if err != nil || got.IgnoredHeadings != 1 || len(got.Requirements) != 1 || got.Requirements[0].ID != "go" || len(got.Matches) != 1 || got.Matches[0].Result != "DIRECT" || got.Score == nil || *got.Score != 100 || got.Coverage != 100 {
		t.Fatal(got, err)
	}
	if len(in.Requirements) != 2 || len(in.Matches) != 2 || in.Requirements[0].ClaimType != "MAJOR_REQUIREMENT" {
		t.Fatal("input document was changed")
	}
	api := normalizeRequirements(in.Requirements, text)
	if len(api) != 1 || api[0].ID != "go" {
		t.Fatal(api)
	}
	for _, label := range []string{"专业领域：", "AI应用技能：", "职业素养：", "知识与技能要求：", "3、能力特质:"} {
		r := Requirement{Category: "REQUIRED", Text: label, Excerpt: label, Confidence: 1}
		if !isSectionHeading(r, label+"\n熟悉 Go") {
			t.Fatal("known standalone heading was kept", label)
		}
	}
}

func TestHeadingRepairNeverDropsConditionsOrFabricatedEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*ChatJob, *string)
	}{
		{"inline condition", func(in *ChatJob, text *string) { *text = "专业能力：熟悉 Go" }},
		{"missing source heading", func(in *ChatJob, text *string) { *text = "熟悉 Go" }},
		{"invented whitespace in excerpt", func(in *ChatJob, text *string) { in.Requirements[0].Excerpt = " 专业能力： " }},
		{"positive proof", func(in *ChatJob, text *string) {
			in.Matches[0].Result = "DIRECT"
			in.Matches[0].Evidence = []Citation{{ID: "language-0", Excerpt: "Go"}}
		}},
		{"forged citation", func(in *ChatJob, text *string) {
			in.Matches[0].Evidence = []Citation{{ID: "missing", Excerpt: "虚构依据"}}
		}},
		{"duplicate judgment", func(in *ChatJob, text *string) { in.Matches = append(in.Matches, in.Matches[0]) }},
		{"forged review note", func(in *ChatJob, text *string) { in.Matches[0].ReviewNote = InvalidAbilityEvidence }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, text, p, c := headingChatFixture()
			tc.change(&in, &text)
			if _, err := ImportChatJob(in, text, d.Job{ID: "job"}, p, c, time.Now()); err == nil {
				t.Fatal("unsafe heading was silently dropped")
			}
		})
	}
	for _, text := range []string{"专业能力：熟悉 Go", "计算机相关专业：", "本科及以上学历：", "熟悉 Go："} {
		r := Requirement{Category: "REQUIRED", Text: text, Excerpt: text, Confidence: 1}
		if isSectionHeading(r, text) {
			t.Fatal("actual condition became formatting", text)
		}
	}
	in, source, _, _ := headingChatFixture()
	in.Requirements[0].Value = "计算机"
	if isSectionHeading(in.Requirements[0], source) || len(omitSectionHeadings(in.Requirements, source)) != 2 {
		t.Fatal("a qualification value was discarded as formatting")
	}
}

func TestErrorsAfterHeadingRepairKeepOriginalPositions(t *testing.T) {
	for _, tc := range []struct {
		reason string
		change func(*ChatJob)
	}{
		{"CHAT_REQUIREMENT_INVALID", func(in *ChatJob) { in.Requirements[1].Excerpt = "Golang" }},
		{"EXCERPT_NOT_EXACT", func(in *ChatJob) { in.Matches[1].Evidence[0].Excerpt = "Golang" }},
		{"CHAT_REQUIREMENT_CONTENT_DUPLICATE", func(in *ChatJob) {
			in.Requirements = append(in.Requirements, in.Requirements[1])
			in.Requirements[2].ID = "duplicate"
		}},
	} {
		in, text, p, c := headingChatFixture()
		tc.change(&in)
		_, err := ImportChatJob(in, text, d.Job{}, p, c, time.Now())
		var v *ValidationError
		if !errors.As(err, &v) || v.Reason != tc.reason {
			t.Fatal(v, err)
		}
		if tc.reason == "CHAT_REQUIREMENT_CONTENT_DUPLICATE" {
			if v.ItemIndex != 3 || v.RelatedItemIndex != 2 {
				t.Fatal(v)
			}
		} else if v.ItemIndex != 2 {
			t.Fatal("error shifted after removing heading", v)
		}
	}
	in, text, p, c := headingChatFixture()
	in.Requirements = in.Requirements[:1]
	in.Matches = in.Matches[:1]
	_, err := ImportChatJob(in, text, d.Job{}, p, c, time.Now())
	var v *ValidationError
	if !errors.As(err, &v) || v.Reason != "CHAT_REQUIREMENTS_EMPTY" {
		t.Fatal("heading-only analysis was accepted", err)
	}
}

func TestChatRepairsOnlyLiteralUnrestrictedMajor(t *testing.T) {
	in, text, p, c := headingChatFixture()
	in.Requirements = append(in.Requirements, Requirement{ID: "major", Category: "QUALIFICATION", Text: "专业不限", Excerpt: "专业不限，有技术洞察力", ClaimType: "MAJOR_REQUIREMENT", Confidence: .95})
	in.Matches = append(in.Matches, Match{RequirementID: "major", Result: "NO_EVIDENCE", Explanation: "本地核对", Evidence: []Citation{}})
	text += "\n专业不限，有技术洞察力"
	got, err := ImportChatJob(in, text, d.Job{}, p, c, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, rule := range got.Qualifications.Results {
		if rule.Rule == "MAJOR_REQUIREMENT" {
			found = true
			if rule.Result != "NOT_APPLICABLE" {
				t.Fatal(rule)
			}
		}
	}
	if !found || len(got.Requirements) != 2 || got.Requirements[1].Value != "不限" || in.Requirements[2].Value != "" {
		t.Fatal(got)
	}
	for _, label := range []string{"专业能力：", "专业不限，有相关实践能力者优先", "计算机相关专业", "不限于计算机专业"} {
		r := Requirement{Category: "QUALIFICATION", ClaimType: "MAJOR_REQUIREMENT", Text: label, Excerpt: label}
		if repairUnrestrictedMajor(r).Value != "" {
			t.Fatal("inferred a missing major", label)
		}
	}
	r := Requirement{Category: "QUALIFICATION", ClaimType: "MAJOR_REQUIREMENT", Text: "专业不限", Excerpt: "专业不限于计算机"}
	if repairUnrestrictedMajor(r).Value != "" {
		t.Fatal("a partial quotation erased a major condition")
	}
	api := normalizeRequirements(in.Requirements, text)
	if len(api) != 2 || api[1].Value != "不限" {
		t.Fatal("API extraction lost literal major normalization", api)
	}
}

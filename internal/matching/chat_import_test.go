package matching

import (
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestChatDistinctQualificationsCanShareSourceSentence(t *testing.T) {
	profile := d.Profile{Degree: "MASTER", Majors: []string{"软件工程"}}
	candidate, err := CandidateFrom(profile, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"本科及以上学历，计算机或软件工程相关专业", "本科及以上学历，计算机或软件工程相关专业优先。"} {
		t.Run(text, func(t *testing.T) {
			in := ChatJob{Requirements: []Requirement{
				{ID: "degree", Category: "QUALIFICATION", Aspect: "TECHNICAL", Text: text, Excerpt: text, ClaimType: "EDUCATION_REQUIREMENT", Value: "BACHELOR", Confidence: 1},
				{ID: "major", Category: "QUALIFICATION", Aspect: "TECHNICAL", Text: text, Excerpt: text, ClaimType: "MAJOR_REQUIREMENT", Value: "计算机|软件工程", Confidence: 1},
			}, Matches: []Match{
				{RequirementID: "degree", Result: "NO_EVIDENCE", Explanation: "交由本地核对", Evidence: []Citation{}},
				{RequirementID: "major", Result: "NO_EVIDENCE", Explanation: "交由本地核对", Evidence: []Citation{}},
			}}
			result, err := ImportChatJob(in, text, d.Job{ID: "job"}, profile, candidate, time.Now())
			if err != nil || len(result.Requirements) != 2 || len(result.Matches) != 2 || result.Requirements[0].Category != "QUALIFICATION" || result.Requirements[0].ClaimType != "EDUCATION_REQUIREMENT" {
				t.Fatal("distinct qualification facets were rejected or merged", result, err)
			}
		})
	}
}

func TestChatPreferredDegreeDoesNotDemoteMandatoryMajor(t *testing.T) {
	profile := d.Profile{Degree: "MASTER", Majors: []string{"软件工程"}}
	candidate, err := CandidateFrom(profile, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	text := "计算机或软件工程相关专业，硕士优先。"
	in := ChatJob{Requirements: []Requirement{
		{ID: "degree", Category: "QUALIFICATION", Text: text, Excerpt: text, ClaimType: "EDUCATION_REQUIREMENT", Value: "MASTER", Confidence: 1},
		{ID: "major", Category: "QUALIFICATION", Text: text, Excerpt: text, ClaimType: "MAJOR_REQUIREMENT", Value: "计算机|软件工程", Confidence: 1},
	}, Matches: []Match{
		{RequirementID: "degree", Result: "NO_EVIDENCE", Explanation: "资料未记录", Evidence: []Citation{}},
		{RequirementID: "major", Result: "NO_EVIDENCE", Explanation: "交由本地核对", Evidence: []Citation{}},
	}}
	result, err := ImportChatJob(in, text, d.Job{ID: "job"}, profile, candidate, time.Now())
	if err != nil || result.Requirements[0].Category != "BONUS" || result.Requirements[1].Category != "QUALIFICATION" || result.Requirements[1].ClaimType != "MAJOR_REQUIREMENT" {
		t.Fatal("qualification preferences leaked between facets", result.Requirements, err)
	}
}

func TestChatDuplicateGuardsDistinguishIDsAndConditions(t *testing.T) {
	profile := d.Profile{Languages: []string{"Go"}}
	candidate, err := CandidateFrom(profile, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, reason string
		change       func(*ChatJob)
		related      int
	}{
		{"empty ID", "CHAT_REQUIREMENT_ID_INVALID", func(in *ChatJob) { in.Requirements[1].ID = " " }, 0},
		{"long ID", "CHAT_REQUIREMENT_ID_INVALID", func(in *ChatJob) { in.Requirements[1].ID = strings.Repeat("r", 49) }, 0},
		{"duplicate ID", "CHAT_REQUIREMENT_ID_DUPLICATE", func(in *ChatJob) { in.Requirements[1].ID = "r1" }, 1},
		{"duplicate condition after normalization", "CHAT_REQUIREMENT_CONTENT_DUPLICATE", func(in *ChatJob) {}, 1},
		{"same facet with different values", "CHAT_REQUIREMENT_CONTENT_DUPLICATE", func(in *ChatJob) {
			for i := range in.Requirements {
				in.Requirements[i] = Requirement{ID: in.Requirements[i].ID, Category: "QUALIFICATION", Text: "本科及以上学历，硕士优先。", Excerpt: "本科及以上学历，硕士优先。", ClaimType: "EDUCATION_REQUIREMENT", Value: []string{"BACHELOR", "MASTER"}[i], Confidence: 1}
			}
		}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := ChatJob{Requirements: []Requirement{
				{ID: "r1", Category: "REQUIRED", Text: "熟悉 Go 优先。", Excerpt: "熟悉 Go 优先。", Confidence: 1},
				{ID: "r2", Category: "BONUS", Aspect: "TECHNICAL", Text: "熟悉 Go 优先。", Excerpt: "熟悉 Go 优先。", Confidence: 1},
			}}
			tc.change(&in)
			_, err := ImportChatJob(in, "熟悉 Go 优先。本科及以上学历，硕士优先。", d.Job{ID: "job"}, profile, candidate, time.Now())
			var validation *ValidationError
			if !errors.As(err, &validation) || validation.Reason != tc.reason || validation.ItemIndex != 2 || validation.RelatedItemIndex != tc.related {
				t.Fatal("duplicate condition escaped or lacked precise positions", validation, err)
			}
		})
	}
}

func TestChatIndependentAbilitiesCanShareSourceExcerpt(t *testing.T) {
	profile := d.Profile{}
	candidate, err := CandidateFrom(profile, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	text := "熟悉 Go 和 Redis"
	in := ChatJob{Requirements: []Requirement{
		{ID: "go", Category: "REQUIRED", Text: "熟悉 Go", Excerpt: text, Confidence: 1},
		{ID: "redis", Category: "REQUIRED", Text: "熟悉 Redis", Excerpt: text, Confidence: 1},
	}, Matches: []Match{
		{RequirementID: "go", Result: "NO_EVIDENCE", Explanation: "资料未记录", Evidence: []Citation{}},
		{RequirementID: "redis", Result: "NO_EVIDENCE", Explanation: "资料未记录", Evidence: []Citation{}},
	}}
	if _, err := ImportChatJob(in, text, d.Job{ID: "job"}, profile, candidate, time.Now()); err != nil {
		t.Fatal("sharing source context is not a duplicate ability", err)
	}
	in.Matches[1].RequirementID = "go"
	var validation *ValidationError
	if _, err := ImportChatJob(in, text, d.Job{ID: "job"}, profile, candidate, time.Now()); !errors.As(err, &validation) || validation.Reason != "REQUIREMENT_DUPLICATE" {
		t.Fatal("accepted two judgments for one requirement", err)
	}
}

func TestChatImportChecksCitationsAndRecomputesLocalJudgments(t *testing.T) {
	p := d.Profile{Degree: "MASTER", GraduationYear: 2027, Languages: []string{"Go"}, PreferredCities: []string{"上海"}}
	c, err := CandidateFrom(p, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	in := ChatJob{ID: "job", Requirements: []Requirement{{ID: "go", Category: "REQUIRED", Text: "熟悉 Go", Excerpt: "熟悉 Go", Confidence: 1}, {ID: "soft", Category: "REQUIRED", Aspect: "TECHNICAL", Text: "热爱技术", Excerpt: "热爱技术", Confidence: 1}}, Matches: []Match{{RequirementID: "go", Result: "DIRECT", Explanation: "已记录 Go", Evidence: []Citation{{ID: "language-0", Excerpt: "Go"}}}, {RequirementID: "soft", Result: "NO_EVIDENCE", Explanation: "未记录相关事例", Evidence: []Citation{}}}}
	text := "熟悉 Go，热爱技术"
	result, err := ImportChatJob(in, text, d.Job{ID: "job"}, p, c, time.Now())
	if err != nil || *result.Score != 100 || result.Coverage != 100 || result.Source != ChatSource || result.Breakdown[3].Total != 1 {
		t.Fatal(result, err)
	}
	in.Matches[0].Evidence[0].Excerpt = "Golang"
	if _, err = ImportChatJob(in, text, d.Job{ID: "job"}, p, c, time.Now()); err == nil {
		t.Fatal("accepted paraphrased citation")
	}
	in.Matches[0].Evidence = []Citation{{ID: "preferred-city-0", Excerpt: "上海"}}
	// Fixture fact ID spelling is taken from the candidate, not guessed.
	for _, f := range c.Facts {
		if f.Kind == "CITY_PREFERRED" {
			in.Matches[0].Evidence[0].ID = f.ID
		}
	}
	result, err = ImportChatJob(in, text, d.Job{ID: "job"}, p, c, time.Now())
	if err != nil || result.Matches[0].ReviewNote != InvalidAbilityEvidence || result.Score != nil {
		t.Fatal(result, err)
	}
	in.Matches[0].ReviewNote = InvalidAbilityEvidence
	if _, err = ImportChatJob(in, text, d.Job{ID: "job"}, p, c, time.Now()); err == nil {
		t.Fatal("accepted forged internal note")
	}
}

func TestChatAtomicRequirementCanKeepFullMixedSourceContext(t *testing.T) {
	p := d.Profile{Languages: []string{"Go"}}
	c, err := CandidateFrom(p, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	text := "热爱技术，熟悉 Go"
	in := ChatJob{Requirements: []Requirement{{ID: "go", Category: "REQUIRED", Text: "熟悉 Go", Excerpt: text, Confidence: 1}}, Matches: []Match{{RequirementID: "go", Result: "DIRECT", Explanation: "已记录 Go", Evidence: []Citation{{ID: "language-0", Excerpt: "Go"}}}}}
	r, err := ImportChatJob(in, text, d.Job{ID: "job"}, p, c, time.Now())
	if err != nil || *r.Score != 100 {
		t.Fatal(r, err)
	}
	in.Requirements[0].Text = text
	if _, err = ImportChatJob(in, text, d.Job{ID: "job"}, p, c, time.Now()); err == nil {
		t.Fatal("copied a combined judgment into atomic abilities")
	}
}

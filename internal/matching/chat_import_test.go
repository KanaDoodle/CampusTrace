package matching

import (
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"testing"
	"time"
)

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

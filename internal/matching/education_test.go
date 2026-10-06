package matching

import (
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestMatchingUsesSelectedGraduationAndDegreeSpecificMajors(t *testing.T) {
	p := d.Profile{Educations: []d.Education{{ID: "b", Degree: "BACHELOR", Majors: []string{"计算机"}, GraduationYear: 2024, Status: "GRADUATED"}, {ID: "m", Degree: "MASTER", Majors: []string{"数学"}, GraduationYear: 2027, Status: "ENROLLED"}}, PrimaryEducationID: "m"}
	c, err := CandidateFrom(p, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range c.Facts {
		seen[f.Text] = true
	}
	if !seen["2027"] || !seen["MASTER"] || !seen["本科专业：计算机"] || !seen["硕士专业：数学"] {
		t.Fatal(c)
	}
	for _, f := range ModelCandidate(c, ComparisonAbilities).Facts {
		if f.Kind == "EDUCATION" || f.Kind == "GRADUATION" {
			t.Fatal("dates entered paid ability identity")
		}
	}
	reqs := []Requirement{{ID: "g", Category: "QUALIFICATION", ClaimType: "GRADUATION_REQUIREMENT", Value: "2027", Confidence: 1}, {ID: "d", Category: "QUALIFICATION", ClaimType: "EDUCATION_REQUIREMENT", Value: "MASTER", Confidence: 1}, {ID: "major", Category: "QUALIFICATION", ClaimType: "MAJOR_REQUIREMENT", Value: "数学", Excerpt: "本科专业须为数学", Confidence: 1}}
	result := Qualification(d.Job{}, p, reqs, time.Time{})
	for _, r := range result.Results {
		if r.Rule == "MAJOR_REQUIREMENT" && (r.Result != "UNKNOWN" || r.Candidate != "计算机") {
			t.Fatal(r)
		}
		if (r.Rule == "GRADUATION_REQUIREMENT" || r.Rule == "EDUCATION_REQUIREMENT") && r.Result != "PASS" {
			t.Fatal(r)
		}
	}
	p.PrimaryEducationID = "b"
	other, _ := CandidateFrom(p, nil, "")
	for _, f := range other.Facts {
		if f.Kind == "GRADUATION" && f.Text != "2024" {
			t.Fatal(f)
		}
	}
	if !strings.Contains(comparisonPrompt, "owning degree") {
		t.Fatal("degree scope missing from model instruction")
	}
	if abilityCitation(Fact{Kind: "EDUCATION"}, Requirement{Category: "REQUIRED"}) {
		t.Fatal("education proved technical ability")
	}
	before := ComparisonCandidateHash(c, ComparisonAbilities)
	p.PrimaryEducationID = "m"
	p.Educations[1].GraduationYear = 2028
	changed, _ := CandidateFrom(p, nil, "")
	if ComparisonCandidateHash(changed, ComparisonAbilities) != before {
		t.Fatal("graduation-only edit invalidated ability comparison")
	}
}

package rules

import (
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

func TestTechnologyRequirementsPreserveLiteralPlusAndAliases(t *testing.T) {
	for _, tc := range []struct {
		requirement string
		skills      []string
		want        string
	}{
		{"REQUIRED:c++", []string{"C++"}, "PASS"},
		{"REQUIRED:c++", []string{"cpp"}, "PASS"},
		{"REQUIRED:go", []string{"Golang"}, "PASS"},
		{"REQUIRED:go+c|c++", []string{"Go", "C++"}, "PASS"},
		{"REQUIRED:go+c|c++", []string{"Go", "C"}, "PASS"},
		{"REQUIRED:go+c|c++", []string{"Go"}, "FAIL"},
		{"REQUIRED:c+++go", []string{"Go", "C++"}, "PASS"},
		{"REQUIRED:c++|rust", []string{"Rust"}, "PASS"},
		{"REQUIRED:go++java", []string{"Go", "Java"}, "UNKNOWN"},
		{"REQUIRED:go|", []string{"Go"}, "UNKNOWN"},
		{"UNCERTAIN:go|rust+c++", []string{"Go", "Rust", "C++"}, "UNKNOWN"},
		{"c++", []string{"Go"}, "NOT_APPLICABLE"},
	} {
		t.Run(tc.requirement+"/"+d.JSON(tc.skills), func(t *testing.T) {
			es := []d.Evidence{{ID: "tech", Claim: d.Claim{Type: "TECH_STACK", Value: tc.requirement, Confidence: 1}}}
			assessment := Eligibility(d.Job{}, d.Profile{Skills: tc.skills}, es, time.Now())
			for _, result := range assessment.Results {
				if result.Rule == "TECH_STACK" {
					if result.Result != tc.want {
						t.Fatalf("got %s, want %s: %+v", result.Result, tc.want, result)
					}
					return
				}
			}
			t.Fatal("no technology rule result")
		})
	}
}

func TestGoFitDistinguishesGoChoiceFromOtherLanguageChoice(t *testing.T) {
	es := []d.Evidence{{Claim: d.Claim{Type: "TECH_STACK", Value: "REQUIRED:go+c|c++", Confidence: 1}}}
	if got := GoFit(es); got != "EXPLICIT_GO" {
		t.Fatal("a separate C/C++ choice must not make Go optional", got)
	}
}

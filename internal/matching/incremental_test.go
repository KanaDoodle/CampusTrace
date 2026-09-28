package matching

import (
	"reflect"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

func TestComparisonDependenciesKeepReviewSeparateAndInvalidateAllEvidence(t *testing.T) {
	p := d.Profile{Revision: 1, GraduationYear: 2027, Degree: "MASTER", Languages: []string{"Go"}, PreferredCities: []string{"上海"}}
	c, err := CandidateFrom(p, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	reqs := []Requirement{{ID: "go", Category: "REQUIRED"}, {ID: "degree", Category: "QUALIFICATION", ClaimType: "EDUCATION_REQUIREMENT", Value: "BACHELOR"}}
	scope := ComparisonScope(reqs)
	if scope != ComparisonAbilities {
		t.Fatal(scope)
	}
	before := ComparisonCandidateHash(c, scope)
	p.Revision++
	p.GraduationYear = 2028
	p.Degree = "PHD"
	p.PreferredCities = []string{"北京"}
	p.TargetRoles = []string{"基础架构"}
	changed, _ := CandidateFrom(p, nil, "")
	if c.Hash() == changed.Hash() || ComparisonCandidateHash(changed, scope) != before {
		t.Fatal("local edits changed paid identity or failed to change review identity")
	}
	for _, edit := range []func(*d.Profile){func(p *d.Profile) { p.Skills = []string{"Redis"} }, func(p *d.Profile) { p.Languages = []string{"Java"} }, func(p *d.Profile) { p.Majors = []string{"计算机"} }, func(p *d.Profile) { p.ExperienceMonths = 12 }} {
		next := p
		edit(&next)
		altered, _ := CandidateFrom(next, nil, "")
		if ComparisonCandidateHash(altered, scope) == before {
			t.Fatal("new non-cited evidence did not invalidate whole comparison")
		}
	}
	for _, kind := range []string{"IMPLEMENTED", "LIMITATION"} {
		altered, _ := CandidateFrom(p, []d.ProjectFact{{ID: "new", Kind: kind, Verified: true, Claim: "实现 Go 服务"}}, "")
		if ComparisonCandidateHash(altered, scope) == before {
			t.Fatal("new fact or limitation did not invalidate", kind)
		}
	}
	unknown := append(append([]Requirement{}, reqs...), Requirement{ID: "unknown", Category: "QUALIFICATION", Text: "特定学历经历"})
	if ComparisonScope(unknown) != ComparisonFull || ComparisonCandidateHash(c, ComparisonFull) == ComparisonCandidateHash(changed, ComparisonFull) {
		t.Fatal("unknown qualification lost conservative dependencies")
	}
	if ComparisonKey("jd-one", before, scope) == ComparisonKey("jd-two", before, scope) {
		t.Fatal("changed job text/model did not invalidate")
	}
	r := Result{RequirementsKey: "jd-one", ComparisonScope: scope, ComparisonKey: ComparisonKey("jd-one", before, scope)}
	if !r.CanReuse("jd-one", map[string]string{scope: before}) || r.CanReuse("jd-two", map[string]string{scope: before}) || r.CanReuse("jd-one", map[string]string{}) {
		t.Fatal("incomplete identity was reusable")
	}
	r.ComparisonKey = "old-comparison-version"
	if r.CanReuse("jd-one", map[string]string{scope: before}) {
		t.Fatal("old comparison policy remained current")
	}
}

func TestLocalRefreshUpdatesConditionsPreservesAnalysisAndRejectsBadProof(t *testing.T) {
	p := d.Profile{GraduationYear: 2027, Degree: "MASTER", Languages: []string{"Go"}, PreferredCities: []string{"上海"}}
	c, _ := CandidateFrom(p, nil, "")
	reqs := []Requirement{
		{ID: "go", Category: "REQUIRED", Confidence: 1},
		{ID: "year", Category: "QUALIFICATION", ClaimType: "GRADUATION_REQUIREMENT", Value: "2027", Confidence: 1},
		{ID: "degree", Category: "QUALIFICATION", ClaimType: "EDUCATION_REQUIREMENT", Value: "BACHELOR", Confidence: 1},
		{ID: "city", Category: "QUALIFICATION", ClaimType: "LOCATION", Value: "上海", Confidence: 1},
	}
	proof := []Match{{RequirementID: "go", Result: "DIRECT", Explanation: "明确记录 Go", Evidence: []Citation{{"language-0", "Go"}}}}
	matches, err := AssembleMatches(p, c, reqs, proof)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range matches {
		if m.Result != "DIRECT" {
			t.Fatal(m)
		}
	}
	at := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	r := Result{InputKey: "old", ComparisonScope: ComparisonAbilities, AnalyzedAt: at, Requirements: reqs, Matches: matches, CandidateFacts: c.Facts}
	old := d.JSON(r)
	p.GraduationYear = 2028
	p.Degree = "ASSOCIATE"
	p.PreferredCities = []string{"北京"}
	c, _ = CandidateFrom(p, nil, "")
	got, err := RefreshResult(r, d.Job{}, p, c, "new", c.Hash(), at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !got.LocallyRefreshed || got.AnalyzedAt != at || got.Score == nil || *got.Score != 100 || got.Coverage != 100 || got.Matches[1].Result != "MISMATCH" || got.Matches[2].Result != "MISMATCH" || got.Matches[3].Result != "NO_EVIDENCE" || !reflect.DeepEqual(got.Matches[0], proof[0]) {
		t.Fatal(got)
	}
	if d.JSON(r) != old {
		t.Fatal("read-only refresh mutated history")
	}
	r.Matches = []Match{{RequirementID: "go", Result: "DIRECT", Explanation: "伪造引用", Evidence: []Citation{{"language-0", "Kafka"}}}}
	if _, err := RefreshResult(r, d.Job{}, p, c, "new", c.Hash(), at); err == nil {
		t.Fatal("invalid reused proof accepted")
	}
}

func TestLocalQualificationUnknownConflictingAndBoundaryCases(t *testing.T) {
	p := d.Profile{GraduationFrom: 2026, GraduationTo: 2027, Degree: "MASTER", PreferredCities: []string{"上海"}}
	c, _ := CandidateFrom(p, nil, "")
	for _, tt := range []struct {
		reqs   []Requirement
		result string
	}{
		{[]Requirement{{ID: "a", Category: "QUALIFICATION", ClaimType: "GRADUATION_REQUIREMENT", Value: "2027", Confidence: 1}}, "NO_EVIDENCE"},
		{[]Requirement{{ID: "a", Category: "QUALIFICATION", ClaimType: "GRADUATION_REQUIREMENT", Value: "2026-2027", Confidence: 1}}, "DIRECT"},
		{[]Requirement{{ID: "a", Category: "QUALIFICATION", ClaimType: "EDUCATION_REQUIREMENT", Value: "BACHELOR", Confidence: .7}}, "NO_EVIDENCE"},
		{[]Requirement{{ID: "a", Category: "QUALIFICATION", ClaimType: "LOCATION", Value: "上海", Confidence: .7}}, "NO_EVIDENCE"},
		{[]Requirement{{ID: "a", Category: "QUALIFICATION", ClaimType: "EDUCATION_REQUIREMENT", Value: "BACHELOR", Confidence: 1}, {ID: "b", Category: "QUALIFICATION", ClaimType: "EDUCATION_REQUIREMENT", Value: "PHD", Confidence: 1}}, "NO_EVIDENCE"},
	} {
		matches, err := AssembleMatches(p, c, tt.reqs, nil)
		if err != nil || matches[0].Result != tt.result {
			t.Fatal(matches, err, tt.result)
		}
	}
}

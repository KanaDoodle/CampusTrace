package rules

import (
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"testing"
	"time"
)

func fixture() (time.Time, d.Observation, []d.Evidence) {
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	o := d.Observation{ID: "o", PostingID: "p", JobID: "j", ObservedAt: now, FetchStatus: "SUCCESS", Trust: "OFFICIAL", Text: "Apply now"}
	es := []d.Evidence{}
	for _, c := range []d.Claim{{Type: "APPLY_ACTION", Value: "PRESENT", Excerpt: "Apply now"}, {Type: "GRADUATION_REQUIREMENT", Value: "2027"}, {Type: "EDUCATION_REQUIREMENT", Value: "BACHELOR"}, {Type: "JOB_TYPE", Value: "FULL_TIME"}, {Type: "TECH_STACK", Value: "REQUIRED:go"}, {Type: "LOCATION", Value: "Shanghai"}} {
		c.Confidence = 1
		es = append(es, d.Evidence{ID: c.Type, ObservationID: o.ID, JobID: "j", Claim: c})
	}
	return now, o, es
}
func TestStatus(t *testing.T) {
	now, o, es := fixture()
	tests := []struct {
		name   string
		mutate func(*d.Observation, *[]d.Evidence)
		want   string
	}{
		{"official apply", func(*d.Observation, *[]d.Evidence) {}, "OPEN"}, {"200 alone", func(_ *d.Observation, e *[]d.Evidence) { *e = nil }, "NEEDS_VERIFICATION"}, {"403", func(o *d.Observation, _ *[]d.Evidence) { o.FetchStatus = "BLOCKED"; o.HTTPStatus = 403 }, "UNKNOWN"}, {"timeout", func(o *d.Observation, _ *[]d.Evidence) { o.FetchStatus = "TIMEOUT" }, "UNKNOWN"}, {"third party", func(o *d.Observation, _ *[]d.Evidence) { o.Trust = "THIRD_PARTY" }, "NEEDS_VERIFICATION"}, {"expired", func(_ *d.Observation, e *[]d.Evidence) {
			*e = append(*e, d.Evidence{ID: "deadline", ObservationID: "o", Claim: d.Claim{Type: "DEADLINE", Value: "2026-09-08", Confidence: 1}})
		}, "CLOSED"}, {"stale", func(o *d.Observation, _ *[]d.Evidence) { o.ObservedAt = now.Add(-8 * 24 * time.Hour) }, "UNKNOWN"}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oo := o
			ee := append([]d.Evidence{}, es...)
			tt.mutate(&oo, &ee)
			a := Status("j", []d.Observation{oo}, ee, now)
			if a.Status != tt.want {
				t.Fatalf("%+v want %s", a, tt.want)
			}
			if a.RuleVersion == "" {
				t.Fatal("version missing")
			}
		})
	}
	newer := o
	newer.ID = "new"
	newer.ObservedAt = now.Add(time.Second)
	newer.FetchStatus = "BLOCKED"
	if got := Status("j", []d.Observation{o, newer}, es, now.Add(time.Second)); got.Status != "UNKNOWN" {
		t.Fatal("stale evidence used", got)
	}
}
func TestEligibility(t *testing.T) {
	now, _, es := fixture()
	p := d.Profile{UserID: "u", GraduationYear: 2027, Degree: "BACHELOR", PreferredTypes: []string{"FULL_TIME"}, PreferredCities: []string{"Shanghai"}, Skills: []string{"go"}}
	j := d.Job{ID: "j"}
	for _, tt := range []struct {
		name string
		p    d.Profile
		es   []d.Evidence
		want string
	}{{"pass", p, es, "ELIGIBLE"}, {"missing", p, nil, "UNKNOWN"}, {"grad fail", func() d.Profile { x := p; x.GraduationYear = 2026; return x }(), es, "INELIGIBLE"}, {"degree fail", func() d.Profile { x := p; x.Degree = "ASSOCIATE"; return x }(), es, "INELIGIBLE"}, {"acceptable city", func() d.Profile {
		x := p
		x.PreferredCities = []string{"Beijing"}
		x.AcceptableCities = []string{"Shanghai"}
		return x
	}(), es, "CONDITIONAL"}, {"unknown degree", func() d.Profile { x := p; x.Degree = ""; return x }(), es, "UNKNOWN"}, {"tech fail", func() d.Profile { x := p; x.Skills = []string{"python"}; return x }(), es, "INELIGIBLE"}} {
		t.Run(tt.name, func(t *testing.T) {
			a := Eligibility(j, tt.p, tt.es, now)
			if a.Status != tt.want {
				t.Fatalf("%s want %s: %+v", a.Status, tt.want, a.Results)
			}
			if len(a.Results) != 8 {
				t.Fatal("missing dimensions")
			}
		})
	}
}
func TestFitAndRank(t *testing.T) {
	now, _, es := fixture()
	if GoFit(es) != "EXPLICIT_GO" {
		t.Fatal(GoFit(es))
	}
	if GoFit(nil) != "UNKNOWN" {
		t.Fatal("unknown")
	}
	for _, tt := range []struct{ v, w string }{{"go|java|c++", "LANGUAGE_FLEXIBLE"}, {"REQUIRED:java+spring", "NO_GO_SIGNAL"}} {
		e := []d.Evidence{{Claim: d.Claim{Type: "TECH_STACK", Value: tt.v, Confidence: 1}}}
		if GoFit(e) != tt.w {
			t.Fatal(tt)
		}
	}
	conflict := append(append([]d.Evidence{}, es...), d.Evidence{Claim: d.Claim{Type: "TECH_STACK", Value: "REQUIRED:java+spring", Confidence: 1}})
	if GoFit(conflict) != "CONFLICTING" {
		t.Fatal("conflict")
	}
	p := d.Profile{UserID: "u", PreferredCities: []string{"Shanghai"}, TargetRoles: []string{"backend"}, PreferredTypes: []string{"FULL_TIME"}}
	j := d.Job{ID: "j", Title: "Go backend", JobType: "FULL_TIME", Locations: []string{"Shanghai"}, UpdatedAt: now}
	r := Rank(j, p, "OPEN", d.Eligibility{Status: "ELIGIBLE"}, "EXPLICIT_GO", now, DefaultWeights())
	if r.Score != 100 {
		t.Fatal(r)
	}
	sum := 0.0
	for _, v := range r.Breakdown {
		sum += v
	}
	if sum != r.Score {
		t.Fatal("unexplained score")
	}
}
func TestCanonicalAndChanges(t *testing.T) {
	a := Fingerprint("Company", "Go Backend", "FULL_TIME", []string{"Shanghai", "Beijing"}, "a b")
	b := Fingerprint(" company ", "go backend", "FULL_TIME", []string{"beijing", "shanghai"}, "a b")
	if a != b {
		t.Fatal("normalization")
	}
	if a == Fingerprint("company", "go backend", "FULL_TIME", []string{"Beijing", "Shanghai"}, "A   B") {
		t.Fatal("content bytes must remain distinct")
	}

	if a == Fingerprint("company", "go backend", "FULL_TIME", []string{"beijing", "shanghai"}, "changed") {
		t.Fatal("overmerge")
	}
	old := d.Observation{ID: "a", Hash: "a", FetchStatus: "SUCCESS"}
	new := d.Observation{ID: "b", Hash: "b", FetchStatus: "SUCCESS"}
	es := []d.Evidence{{Claim: d.Claim{Type: "LOCATION", Value: "Shanghai"}}}
	changes := Changes(old, new, nil, es)
	if len(changes) != 2 {
		t.Fatal(changes)
	}
	new.Hash = "a"
	if len(Changes(old, new, nil, es)) != 0 {
		t.Fatal("same hash")
	}
}

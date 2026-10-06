package integration

import (
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func TestSavedComparisonLocalPrecisionIsReadOnlyAndMonthEditsReuseAbilities(t *testing.T) {
	ctx, s, _, u, _ := setup(t)
	profile := d.Profile{Educations: []d.Education{{ID: "master", Degree: "MASTER", GraduationYear: 2027, Majors: []string{"软件工程"}, Status: "ENROLLED"}}, PrimaryEducationID: "master", Languages: []string{"Go"}}
	must(t, s.SaveProfile(ctx, u, profile))
	text := "2027届应届毕业生，本科及以上学历\n掌握 Go\n毕业范围开始日期：2026-09-01 00:00:00\n毕业范围结束日期：2027-08-01 00:00:00"
	o, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Precision synthetic", Title: "后端开发工程师", JobType: "FULL_TIME", ExternalID: d.ID(), Text: text, FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, err)
	identity := matching.ModelIdentity("server-default", "fixture")
	snap, err := s.MatchSnapshot(ctx, u, identity, "", []string{o.JobID})
	must(t, err)
	input := snap.Jobs[0]
	reqs := []matching.Requirement{{ID: "grad", Category: "QUALIFICATION", Text: "2027届应届毕业生，本科及以上学历", Excerpt: "2027届应届毕业生，本科及以上学历", ClaimType: "GRADUATION_REQUIREMENT", Value: "2027", Confidence: 1}, {ID: "go", Category: "REQUIRED", Text: "掌握 Go", Excerpt: "掌握 Go", Confidence: 1}}
	proof, err := matching.AssembleMatches(snap.Profile, snap.Candidate, reqs, []matching.Match{{RequirementID: "go", Result: "DIRECT", Explanation: "已记录 Go", Evidence: []matching.Citation{{ID: "language-0", Excerpt: "Go"}}}})
	must(t, err)
	score, coverage := matching.Score(reqs, proof)
	old := matching.Result{JobID: o.JobID, InputKey: input.InputKey, RequirementsKey: input.RequirementsKey, CandidateHash: snap.CandidateHash, ComparisonScope: matching.ComparisonAbilities, ComparisonKey: matching.ComparisonKey(input.RequirementsKey, matching.ComparisonCandidateHash(snap.Candidate, matching.ComparisonAbilities), matching.ComparisonAbilities), Model: identity, AnalyzedAt: time.Now().UTC(), Requirements: reqs, Matches: proof, CandidateFacts: snap.Candidate.Facts, Score: score, Coverage: coverage, Breakdown: matching.ScoreBreakdown(reqs, proof)}
	must(t, s.SaveMatchResult(ctx, u, "", old))
	assertView := func(want string) {
		t.Helper()
		current, err := s.MatchDecisionSnapshot(ctx, u, identity, "", []string{o.JobID}, "")
		must(t, err)
		v := current.Jobs[0]
		if v.State != "ANALYZED" || len(v.Result.Requirements) != 3 || current.CallsToday != 0 || v.Result.AnalyzedAt != old.AnalyzedAt || *v.Score != 100 || len(v.Breakdown) != 4 {
			t.Fatal(v, current.CallsToday)
		}
		for _, r := range v.Result.Qualifications.Results {
			if r.Rule == "GRADUATION_REQUIREMENT" && r.Result != want {
				t.Fatal(r)
			}
			if r.Rule == "EDUCATION_REQUIREMENT" && r.Result != "PASS" {
				t.Fatal(r)
			}
		}
		stored, err := s.MatchResult(ctx, u, o.JobID)
		must(t, err)
		if d.JSON(stored) != d.JSON(old) {
			t.Fatal("read refreshed stored history")
		}
	}
	assertView("UNKNOWN")
	profile.Educations[0].GraduationMonth = 6
	must(t, s.SaveProfile(ctx, u, profile))
	assertView("PASS")
	profile.Educations[0].GraduationMonth = 10
	must(t, s.SaveProfile(ctx, u, profile))
	assertView("FAIL")
	profile.Educations[0].GraduationMonth = 13
	if s.SaveProfile(ctx, u, profile) != p.ErrValidation {
		t.Fatal("invalid graduation month was saved")
	}
}

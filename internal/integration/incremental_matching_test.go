package integration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
)

type scopeMatchModel struct {
	inner  matchFixtureModel
	scopes []bool
}

func (m *scopeMatchModel) Complete(ctx context.Context, messages, tools any) (json.RawMessage, error) {
	var msgs []map[string]string
	if err := json.Unmarshal([]byte(d.JSON(messages)), &msgs); err != nil {
		return nil, err
	}
	m.scopes = append(m.scopes, strings.Contains(msgs[1]["content"], `｜GRADUATION】`))
	return m.inner.Complete(ctx, messages, tools)
}

func TestWholeMatchingReadsCompleteScopeRegardlessOfLegacyRequirements(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	profile := d.Profile{GraduationYear: 2027, Degree: "MASTER", Languages: []string{"Go"}}
	must(t, s.SaveProfile(ctx, u, profile))
	ids := []string{}
	identity := matching.ModelIdentity("server-default", "fixture")
	for i, text := range []string{"熟悉 Go", "熟悉 Go，具备在校科研经历"} {
		o, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Scope synthetic", Title: "服务端开发", JobType: "FULL_TIME", Locations: []string{"上海"}, ExternalID: d.ID(), Text: text, FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
		must(t, err)
		ids = append(ids, o.JobID)
		snap, err := s.MatchSnapshot(ctx, u, identity, "", []string{o.JobID})
		must(t, err)
		reqs := []matching.Requirement{{ID: "go", Category: "REQUIRED", Text: "熟悉 Go", Excerpt: "熟悉 Go", Confidence: 1}}
		if i == 1 {
			reqs = append(reqs, matching.Requirement{ID: "unknown", Category: "QUALIFICATION", Text: "具备在校科研经历", Excerpt: "具备在校科研经历", Confidence: 1})
		}
		must(t, s.SaveRequirements(ctx, u, snap.Jobs[0].RequirementsKey, matching.Requirements{Items: reqs}))
	}
	authn := auth.Service{Store: s, Secret: []byte("scope-fixture-secret-at-least-32-bytes")}
	token, err := authn.Token(u)
	must(t, err)
	model := &scopeMatchModel{}
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, ResumeModel: model, ResumeModelName: "fixture"}).Handler()
	snap, err := s.MatchSnapshot(ctx, u, identity, "", ids)
	must(t, err)
	rec := matchingRequest(handler, token, "/api/matching/analyze", "POST", map[string]any{"job_ids": ids, "candidate_hash": snap.CandidateHash})
	if rec.Code != 200 || len(model.scopes) != 1 || !model.scopes[0] {
		t.Fatal(rec.Code, rec.Body.String(), model.scopes)
	}
	profile.GraduationYear = 2028
	must(t, s.SaveProfile(ctx, u, profile))
	snap, err = s.MatchSnapshot(ctx, u, identity, "", ids)
	must(t, err)
	for _, j := range snap.Jobs {
		want := "STALE"
		if j.State != want {
			t.Fatal("unknown gate was incorrectly reused", j.State, want)
		}
	}
	rec = matchingRequest(handler, token, "/api/matching/analyze", "POST", map[string]any{"job_ids": ids, "candidate_hash": snap.CandidateHash})
	if rec.Code != 200 || len(model.scopes) != 2 || !model.scopes[1] {
		t.Fatal(rec.Code, rec.Body.String(), model.scopes)
	}
}

func TestIncrementalMatchingReadOnlyLocalUpdatesKeepPaidEvidenceAndFences(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	profile := d.Profile{GraduationYear: 2027, Degree: "MASTER", Languages: []string{"Go"}, PreferredCities: []string{"上海"}, TargetRoles: []string{"后端开发"}, PreferredTypes: []string{"FULL_TIME"}}
	must(t, s.SaveProfile(ctx, u, profile))
	text := "掌握 Go\n2027届毕业生\n本科及以上\n工作地点：上海\n全职岗位"
	o, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Incremental synthetic", Title: "服务端开发", JobType: "FULL_TIME", Locations: []string{"上海"}, Text: text, FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, err)
	model := &matchFixtureModel{}
	authn := auth.Service{Store: s, Secret: []byte("incremental-fixture-secret-32-bytes")}
	token, err := authn.Token(u)
	must(t, err)
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New(), ResumeModel: model, ResumeModelName: "fixture"}).Handler()
	identity := matching.ModelIdentity("server-default", "fixture")
	snapshot := func() p.MatchSnapshot {
		t.Helper()
		snap, err := s.MatchSnapshot(ctx, u, identity, "", []string{o.JobID})
		must(t, err)
		return snap
	}
	first := snapshot()
	reqs := []matching.Requirement{
		{ID: "go", Category: "REQUIRED", Text: "掌握 Go", Excerpt: "掌握 Go", Confidence: 1},
		{ID: "year", Category: "QUALIFICATION", Text: "2027届毕业生", Excerpt: "2027届毕业生", ClaimType: "GRADUATION_REQUIREMENT", Value: "2027", Confidence: 1},
		{ID: "degree", Category: "QUALIFICATION", Text: "本科及以上", Excerpt: "本科及以上", ClaimType: "EDUCATION_REQUIREMENT", Value: "BACHELOR", Confidence: 1},
		{ID: "city", Category: "QUALIFICATION", Text: "工作地点：上海", Excerpt: "工作地点：上海", ClaimType: "LOCATION", Value: "上海", Confidence: 1},
	}
	must(t, s.SaveRequirements(ctx, u, first.Jobs[0].RequirementsKey, matching.Requirements{Items: reqs}))
	analyze := func(hash string) {
		t.Helper()
		rec := matchingRequest(handler, token, "/api/matching/analyze", "POST", map[string]any{"job_ids": []string{o.JobID}, "candidate_hash": hash})
		if rec.Code != 200 {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	analyze(first.CandidateHash)
	if model.calls.Load() != 1 {
		t.Fatal("local gates reached the model", model.calls.Load())
	}
	stored, err := s.MatchResult(ctx, u, o.JobID)
	must(t, err)
	if stored.Holistic == nil || len(stored.Matches) != 0 || stored.Score != nil {
		t.Fatal(stored)
	}
	oldBody := d.JSON(stored)
	profile.PreferredCities = []string{"北京"}
	profile.GraduationYear = 2028
	profile.Degree = "PHD"
	must(t, s.SaveProfile(ctx, u, profile))
	next := snapshot()
	if next.CandidateHash == first.CandidateHash || next.Jobs[0].State != "STALE" || next.Jobs[0].Score != nil {
		t.Fatal(next)
	}
	// Old review authorization is stale even though the paid result is reusable.
	rec := matchingRequest(handler, token, "/api/matching/analyze", "POST", map[string]any{"job_ids": []string{o.JobID}, "candidate_hash": first.CandidateHash})
	if rec.Code != 409 || model.calls.Load() != 1 {
		t.Fatal("old consent lost its fence", rec.Code)
	}
	read := func() matching.Result {
		t.Helper()
		rec := matchingRequest(handler, token, "/api/matching/results/"+o.JobID, "POST", map[string]any{})
		var v struct {
			State  string          `json:"state"`
			Result matching.Result `json:"result"`
		}
		must(t, json.Unmarshal(rec.Body.Bytes(), &v))
		if rec.Code != 200 || v.State != "STALE" || v.Result.Holistic == nil || v.Result.AnalyzedAt != stored.AnalyzedAt {
			t.Fatal(rec.Code, rec.Body.String())
		}
		return v.Result
	}
	refreshed := read()
	for _, path := range []string{"/api/matching/company", "/api/matching/preparation/" + o.JobID} {
		body := map[string]any{}
		if path == "/api/matching/company" {
			body["company"] = "Incremental synthetic"
		}
		rec := matchingRequest(handler, token, path, "POST", body)
		if rec.Code != 200 {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	unchanged, err := s.MatchResult(ctx, u, o.JobID)
	must(t, err)
	if model.calls.Load() != 1 || d.JSON(unchanged) != oldBody || snapshot().CallsToday != 1 {
		t.Fatal("local read/reuse mutated history or consumed budget")
	}
	if err := s.SaveMatchResult(ctx, u, "", stored); !errors.Is(err, p.ErrStaleInput) {
		t.Fatal("old writer committed", err)
	}
	// New skills and even previously non-cited project facts invalidate the
	// entire model comparison, including old NO_EVIDENCE conclusions.
	profile.GraduationYear = 2027
	profile.Skills = []string{"Redis"}
	must(t, s.SaveProfile(ctx, u, profile))
	if snapshot().Jobs[0].State != "STALE" {
		t.Fatal("new skill kept comparison")
	}
	analyze(snapshot().CandidateHash)
	if model.calls.Load() != 2 {
		t.Fatal("changed ability reparsed JD or skipped comparison", model.calls.Load())
	}
	project, err := s.SaveProject(ctx, u, d.Project{Name: "Synthetic queue"})
	must(t, err)
	_, err = s.SaveFact(ctx, u, d.ProjectFact{ProjectID: project.ID, Kind: "IMPLEMENTED", Verified: true, Claim: "实现 Redis 任务队列"})
	must(t, err)
	if snapshot().Jobs[0].State != "STALE" {
		t.Fatal("new project fact reused old conclusions")
	}
	if err := s.SaveMatchResult(ctx, u, "", refreshed); !errors.Is(err, p.ErrStaleInput) {
		t.Fatal("refreshed writer lost full input fence", err)
	}
}

func TestWholeMatchingDoesNotDeriveAbilityFromLegacyQualificationOnlyCache(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{GraduationYear: 2027, Degree: "MASTER"}))
	o, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Local gates synthetic", Title: "岗位", JobType: "FULL_TIME", Locations: []string{"上海"}, Text: "2027届毕业生", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, err)
	identity := matching.ModelIdentity("server-default", "fixture")
	snap, err := s.MatchSnapshot(ctx, u, identity, "", []string{o.JobID})
	must(t, err)
	must(t, s.SaveRequirements(ctx, u, snap.Jobs[0].RequirementsKey, matching.Requirements{Items: []matching.Requirement{{ID: "year", Category: "QUALIFICATION", Text: "2027届毕业生", Excerpt: "2027届毕业生", ClaimType: "GRADUATION_REQUIREMENT", Value: "2027", Confidence: 1}}}))
	authn := auth.Service{Store: s, Secret: []byte("local-gates-fixture-secret-32-bytes")}
	token, err := authn.Token(u)
	must(t, err)
	model := &matchFixtureModel{}
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, ResumeModel: model, ResumeModelName: "fixture"}).Handler()
	rec := matchingRequest(handler, token, "/api/matching/analyze", "POST", map[string]any{"job_ids": []string{o.JobID}, "candidate_hash": snap.CandidateHash})
	if rec.Code != 200 || model.calls.Load() != 1 {
		t.Fatal(rec.Code, rec.Body.String(), model.calls.Load())
	}
	result, err := s.MatchResult(ctx, u, o.JobID)
	must(t, err)
	if result.Score != nil || result.Holistic == nil || result.Holistic.Fit != "UNCERTAIN" || len(result.Matches) != 0 {
		t.Fatal(result)
	}
}

package integration

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
)

func TestLocalDirectionHandlesAgentTargetsWithoutCallsAndPreservesPaidResults(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	profile := d.Profile{TargetRoles: []string{"backend", "Agent开发", "服务端"}, Languages: []string{"Go"}}
	must(t, s.SaveProfile(ctx, u, profile))
	ids, want := []string{}, map[string]string{}
	for _, fixture := range []struct{ title, direction string }{
		{"Agent Harness 研发工程师", "MATCH"}, {"AI Infra工程师", "RELATED"}, {"营销专员", "UNRELATED"}, {"会计", "UNRELATED"},
	} {
		o, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Direction synthetic", Title: fixture.title, JobType: "FULL_TIME", ExternalID: d.ID(), Text: "任职要求\n熟悉 Go", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
		must(t, err)
		ids = append(ids, o.JobID)
		want[o.JobID] = fixture.direction
	}
	model := &matchFixtureModel{}
	authn := auth.Service{Store: s, Secret: []byte("direction-fixture-http-secret-at-least-32")}
	token, err := authn.Token(u)
	must(t, err)
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, ResumeModel: model, ResumeModelName: "fixture"}).Handler()
	rec := matchingRequest(handler, token, "/api/matching/preview", "POST", map[string]any{})
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var snap p.MatchSnapshot
	must(t, json.Unmarshal(rec.Body.Bytes(), &snap))
	if len(snap.Jobs) < 4 || model.calls.Load() != 0 || snap.CallsToday != 0 {
		t.Fatal("local screening used a model call or omitted jobs")
	}
	seen := 0
	for _, j := range snap.Jobs {
		if _, own := want[j.Job.ID]; !own {
			continue
		}
		seen++
		if j.Local.Direction.Status != want[j.Job.ID] || j.Local.Version != matching.LocalVersion || j.ExcludedReason != "" {
			t.Fatal(j.Job.Title, j.Local.Direction, j.ExcludedReason)
		}
	}
	if seen != 4 {
		t.Fatal("preview omitted owned fixture jobs")
	}
	rec = matchingRequest(handler, token, "/api/matching/analyze", "POST", map[string]any{"job_ids": ids[:1], "candidate_hash": snap.CandidateHash})
	if rec.Code != 200 || model.calls.Load() != 1 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	profile.TargetRoles = []string{"财务"}
	must(t, s.SaveProfile(ctx, u, profile))
	current, err := s.MatchSnapshot(ctx, u, matching.ModelIdentity("server-default", "fixture"), "", ids)
	must(t, err)
	for _, j := range current.Jobs {
		if j.Job.ID == ids[0] && (j.State != "STALE" || j.Local.Direction.Status != "UNRELATED") {
			t.Fatal("whole assessment failed to invalidate after preference edit", j.State, j.Local.Direction)
		}
		if j.Job.ID == ids[3] && j.Local.Direction.Status != "MATCH" {
			t.Fatal("current target was not used", j.Local.Direction)
		}
	}
	if model.calls.Load() != 1 || current.CallsToday != 1 {
		t.Fatal("local direction refresh incurred another model call")
	}
}

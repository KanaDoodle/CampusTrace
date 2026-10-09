package integration

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
)

func TestMatchingLocalFactsPreferencesAndOwnerScopeWithoutModelCalls(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	profile := d.Profile{Degree: "MASTER", GraduationYear: 2027, Skills: []string{"Go"}, TargetRoles: []string{"后端开发"}, AcceptableCities: []string{"杭州"}}
	must(t, s.SaveProfile(ctx, u, profile))
	now := time.Now().UTC()
	one, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Synthetic local screen", Title: "研发工程师", Locations: []string{"浙江省-杭州市-余杭区", "Hangzhou"}, JobType: "FULL_TIME", ExternalID: d.ID(), Text: "岗位职责\n负责服务接口、数据库和异步任务处理\n任职要求\n熟悉 Go", FetchStatus: "SUCCESS", ObservedAt: now})
	must(t, err)
	two, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Synthetic local screen", Title: "服务端开发", JobType: "FULL_TIME", ExternalID: d.ID(), Text: "熟悉 C++\nGo 加分", FetchStatus: "SUCCESS", ObservedAt: now})
	must(t, err)
	model := &matchFixtureModel{}
	authn := auth.Service{Store: s, Secret: []byte("integration-matching-local-secret-2027")}
	token, err := authn.Token(u)
	must(t, err)
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New(), ResumeModel: model, ResumeModelName: "fixture"}).Handler()
	before, err := s.MatchSnapshot(ctx, u, "model-a", "", []string{one.JobID, two.JobID})
	must(t, err)
	project, err := s.SaveProject(ctx, u, d.Project{Name: "本地队列"})
	must(t, err)
	fact, err := s.SaveFact(ctx, u, d.ProjectFact{ProjectID: project.ID, Kind: "IMPLEMENTED", Verified: true, Claim: "实现 Go 服务接口和异步任务处理"})
	must(t, err)
	after, err := s.MatchSnapshot(ctx, u, "model-a", "", []string{one.JobID, two.JobID})
	must(t, err)
	get := func(v p.MatchSnapshot, id string) p.MatchJob {
		for _, row := range v.Jobs {
			if row.Job.ID == id {
				return row
			}
		}
		t.Fatal("missing row")
		return p.MatchJob{}
	}
	a, b := get(before, one.JobID), get(after, one.JobID)
	if len(b.Cities) != 1 || b.Cities[0] != "杭州" || b.Job.Locations[0] != "浙江省-杭州市-余杭区" || after.CityAliases["Hangzhou"][0] != "杭州" || after.CityAliases["杭州"][0] != "杭州" {
		t.Fatal("city projection differs from preferences or modified source locations", b.Cities, b.Job.Locations, after.CityAliases)
	}
	if b.Local == nil || b.Local.RoleSource != "BODY" || b.PreliminaryScore <= a.PreliminaryScore || after.Jobs[0].Job.ID != one.JobID {
		t.Fatal(a, b, after.Jobs)
	}
	found := false
	for _, check := range b.Local.Checks {
		for _, e := range check.Evidence {
			if e.ID == fact.ID {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("confirmed fact not cited")
	}
	otherModel, err := s.MatchSnapshot(ctx, u, "model-b", "", []string{one.JobID})
	must(t, err)
	if otherModel.Jobs[0].PreliminaryScore != b.PreliminaryScore {
		t.Fatal("local priority depended on model")
	}
	profile.AcceptableCities = nil
	must(t, s.SaveProfile(ctx, u, profile))
	changed, err := s.MatchSnapshot(ctx, u, "model-a", "", []string{one.JobID})
	must(t, err)
	if changed.Jobs[0].PreliminaryScore != b.PreliminaryScore-8 {
		t.Fatal("preference change retained old priority")
	}
	rec := matchingRequest(handler, token, "/api/matching/results/"+one.JobID, "POST", map[string]any{})
	var result struct {
		Local *matching.LocalScreen `json:"local"`
	}
	must(t, json.Unmarshal(rec.Body.Bytes(), &result))
	if rec.Code != 200 || result.Local == nil {
		t.Fatal(rec.Code, rec.Body.String())
	}
	other, err := s.NewUser(ctx, d.ID()+"@local-screen.invalid", "unused")
	must(t, err)
	must(t, s.SaveProfile(ctx, other, profile))
	otherToken, err := authn.Token(other)
	must(t, err)
	rec = matchingRequest(handler, otherToken, "/api/matching/results/"+one.JobID, "POST", map[string]any{})
	if rec.Code != 404 || model.calls.Load() != 0 || changed.CallsToday != 0 {
		t.Fatal("local analysis leaked or incurred calls", rec.Code, model.calls.Load(), changed.CallsToday)
	}
}

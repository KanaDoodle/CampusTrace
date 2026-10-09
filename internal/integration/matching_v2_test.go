package integration

import (
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

func TestMatchingV2PreferencesSectionsProjectContextAndStaleResults(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{GraduationYear: 2027, Degree: "MASTER", Languages: []string{"Go"}, PreferredCities: []string{"Shanghai"}, PreferredTypes: []string{"FULL_TIME"}, Majors: []string{"数学"}}))
	project, err := s.SaveProject(ctx, u, d.Project{Name: "任务队列"})
	must(t, err)
	_, err = s.SaveFact(ctx, u, d.ProjectFact{ProjectID: project.ID, Kind: "IMPLEMENTED", Verified: true, Claim: "使用 SQL 行锁调度任务"})
	must(t, err)
	o, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Synthetic matching v2", Title: "Go 平台开发", JobType: "FULL_TIME", Locations: []string{"上海市"}, Text: "熟悉 Go\n工作地点：上海市、杭州市\n沟通协作顺畅\n软件工程专业优先", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, err)
	authn := auth.Service{Store: s, Secret: []byte("synthetic-matching-v2-http-secret-32")}
	token, err := authn.Token(u)
	must(t, err)
	model := &matchFixtureModel{}
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New(), ResumeModel: model, ResumeModelName: "fixture"}).Handler()
	identity := matching.ModelIdentity("server-default", "fixture")
	snap, err := s.MatchSnapshot(ctx, u, identity, "", []string{o.JobID})
	must(t, err)
	rec := matchingRequest(handler, token, "/api/matching/analyze", "POST", map[string]any{"job_ids": []string{o.JobID}, "candidate_hash": snap.CandidateHash})
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	result, err := s.MatchResult(ctx, u, o.JobID)
	must(t, err)
	if result.Score != nil || result.Coverage != 0 || len(result.Breakdown) != 0 || result.Holistic == nil {
		t.Fatal("whole analysis retained item-count scores", result)
	}
	for _, row := range result.Qualifications.Results {
		if row.Rule == "MAJOR_REQUIREMENT" && row.Result != "NOT_APPLICABLE" {
			t.Fatal("preferred major became gate", row)
		}
	}
	project.Name = "任务调度平台"
	_, err = s.UpdateProject(ctx, u, project.ID, project)
	must(t, err)
	changed, err := s.MatchSnapshot(ctx, u, identity, "", []string{o.JobID})
	must(t, err)
	if changed.CandidateHash == snap.CandidateHash || changed.Jobs[0].State != "STALE" || changed.Jobs[0].RequirementsKey != snap.Jobs[0].RequirementsKey {
		t.Fatal("project rename did not invalidate only personal comparison", changed)
	}
	if err := s.SaveMatchResult(ctx, u, "", result); err != p.ErrStaleInput {
		t.Fatal("stale project context was saved", err)
	}
	rec = matchingRequest(handler, token, "/api/matching/results/"+o.JobID, "POST", map[string]any{})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"state":"STALE"`) || !strings.Contains(rec.Body.String(), `"project_name":"任务队列"`) || model.calls.Load() != 1 {
		t.Fatal("prior result erased or read triggered paid analysis", rec.Code, rec.Body.String(), model.calls.Load())
	}
	// A prior-version result must not be reused even if JD and profile are unchanged.
	result.InputKey = matching.InputKey(d.Hash("matching-v1\n"+identity+"\n"+changed.Jobs[0].Text), changed.CandidateHash)
	_, err = s.DB.ExecContext(ctx, "UPDATE job_match_results SET body=? WHERE user_id=? AND job_id=?", d.JSON(result), u, o.JobID)
	must(t, err)
	versioned, err := s.MatchSnapshot(ctx, u, identity, "", []string{o.JobID})
	must(t, err)
	if versioned.Jobs[0].State != "STALE" || model.calls.Load() != 1 {
		t.Fatal("old version consumed or silently refreshed", versioned.Jobs, model.calls.Load())
	}
}

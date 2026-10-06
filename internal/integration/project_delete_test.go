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

func TestProjectAndFactDeletionAreOwnerScopedAndInvalidateMatching(t *testing.T) {
	ctx, store, queue, owner, _ := setup(t)
	other, err := store.NewUser(ctx, d.ID()+"@synthetic.test", "unused-test-hash")
	must(t, err)
	must(t, store.SaveProfile(ctx, owner, d.Profile{Languages: []string{"Go"}}))
	project, err := store.SaveProject(ctx, owner, d.Project{Name: "待清理的任务队列", Bullets: []string{"完整实现经历"}})
	must(t, err)
	retained, err := store.SaveProject(ctx, owner, d.Project{Name: "保留的服务"})
	must(t, err)
	facts := []d.ProjectFact{}
	for _, id := range []string{project.ID, project.ID, retained.ID} {
		fact, err := store.SaveFact(ctx, owner, d.ProjectFact{ProjectID: id, Kind: "IMPLEMENTED", Claim: "使用 Go 实现服务", Verified: true})
		must(t, err)
		facts = append(facts, fact)
	}
	job, err := store.IngestForUser(ctx, owner, p.Ingest{Company: "Synthetic Deletion", Title: "Go 后端开发", JobType: "FULL_TIME", Locations: []string{"上海市"}, ExternalID: d.ID(), Text: "掌握 Go", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, err)
	authn := auth.Service{Store: store, Secret: []byte("synthetic-delete-check-secret-32")}
	token, err := authn.Token(owner)
	must(t, err)
	otherToken, err := authn.Token(other)
	must(t, err)
	model := &matchFixtureModel{}
	handler := (&transport.API{Store: store, Queue: queue, Auth: authn, Metrics: observability.New(), ResumeModel: model, ResumeModelName: "fixture"}).Handler()
	identity := matching.ModelIdentity("server-default", "fixture")
	before, err := store.MatchSnapshot(ctx, owner, identity, "", []string{job.JobID})
	must(t, err)
	rec := matchingRequest(handler, token, "/api/matching/analyze", "POST", map[string]any{"job_ids": []string{job.JobID}, "candidate_hash": before.CandidateHash})
	if rec.Code != 200 || model.calls.Load() != 2 {
		t.Fatalf("initial analysis: %d %s", rec.Code, rec.Body.String())
	}
	for _, path := range []string{"/api/projects/" + project.ID, "/api/project_facts/" + facts[0].ID} {
		rec := matchingRequest(handler, otherToken, path, "DELETE", nil)
		if rec.Code != 404 {
			t.Fatalf("cross-owner deletion: %d", rec.Code)
		}
	}
	unchanged, err := store.MatchSnapshot(ctx, owner, identity, "", []string{job.JobID})
	must(t, err)
	if unchanged.CandidateHash != before.CandidateHash || unchanged.Jobs[0].State != "ANALYZED" {
		t.Fatal("unauthorized deletion changed matching inputs")
	}
	rec = matchingRequest(handler, token, "/api/project_facts/"+facts[0].ID, "DELETE", nil)
	if rec.Code != 200 {
		t.Fatalf("single fact deletion: %d", rec.Code)
	}
	remaining, err := p.Many[d.ProjectFact](ctx, store.DB, "SELECT body FROM project_facts WHERE user_id=? ORDER BY id", owner)
	must(t, err)
	if len(remaining) != 2 {
		t.Fatal("single deletion removed an identical sibling")
	}
	after, err := store.MatchSnapshot(ctx, owner, identity, "", []string{job.JobID})
	must(t, err)
	if after.CandidateHash == before.CandidateHash || after.Jobs[0].State != "STALE" {
		t.Fatal("deleted evidence retained current matching status")
	}
	_, err = store.MatchResult(ctx, owner, job.JobID)
	must(t, err) // Keep the previous report available as history.
	rec = matchingRequest(handler, token, "/api/matching/analyze", "POST", map[string]any{"job_ids": []string{job.JobID}, "candidate_hash": before.CandidateHash})
	if rec.Code != 409 || model.calls.Load() != 2 {
		t.Fatal("stale confirmation reached the model")
	}
	rec = matchingRequest(handler, token, "/api/projects/"+project.ID, "DELETE", nil)
	var result struct {
		Deleted      bool  `json:"deleted"`
		DeletedFacts int64 `json:"deleted_facts"`
	}
	must(t, json.Unmarshal(rec.Body.Bytes(), &result))
	if rec.Code != 200 || !result.Deleted || result.DeletedFacts != 1 {
		t.Fatalf("project deletion: %d %+v", rec.Code, result)
	}
	projects, err := p.Many[d.Project](ctx, store.DB, "SELECT body FROM projects WHERE user_id=?", owner)
	must(t, err)
	remaining, err = p.Many[d.ProjectFact](ctx, store.DB, "SELECT body FROM project_facts WHERE user_id=?", owner)
	must(t, err)
	if len(projects) != 1 || projects[0].ID != retained.ID || len(remaining) != 1 || remaining[0].ID != facts[2].ID {
		t.Fatal("project deletion left orphaned facts or removed another project")
	}
	if rec = matchingRequest(handler, token, "/api/projects/"+project.ID, "DELETE", nil); rec.Code != 404 {
		t.Fatal("missing project was reported as deleted", rec.Code)
	}
}

package integration

import (
	"encoding/json"
	"fmt"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
	"strings"
	"testing"
	"time"
)

func TestRadarPreviewAndAnalysisAllowCompleteResumeOverTheOldByteLimit(t *testing.T) {
	ctx, store, queue, owner, _ := setup(t)
	must(t, store.SaveProfile(ctx, owner, d.Profile{Degree: "MASTER", GraduationYear: 2027, Languages: []string{"Go"}, TargetRoles: []string{"后端开发"}}))
	project, err := store.SaveProject(ctx, owner, d.Project{Name: "包含完整实现机制和结果的任务队列"})
	must(t, err)
	for i := 0; i < 32; i++ {
		_, err := store.SaveFact(ctx, owner, d.ProjectFact{ProjectID: project.ID, Kind: "IMPLEMENTED", Verified: true, Claim: fmt.Sprintf("任务 %d：", i) + strings.Repeat("实现任务处理与失败重试，", 10)})
		must(t, err)
	}
	observation, err := store.IngestForUser(ctx, owner, p.Ingest{Company: "Synthetic Capacity", Title: "Go 后端开发", JobType: "FULL_TIME", Locations: []string{"上海市"}, ExternalID: d.ID(), Text: "掌握 Go", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, err)
	authn := auth.Service{Store: store, Secret: []byte("synthetic-capacity-check-secret-32")}
	token, err := authn.Token(owner)
	must(t, err)
	model := &matchFixtureModel{}
	handler := (&transport.API{Store: store, Queue: queue, Auth: authn, Metrics: observability.New(), ResumeModel: model, ResumeModelName: "fixture"}).Handler()
	rec := matchingRequest(handler, token, "/api/matching/preview", "POST", map[string]any{})
	var preview p.MatchSnapshot
	must(t, json.Unmarshal(rec.Body.Bytes(), &preview))
	found := false
	for _, job := range preview.Jobs {
		found = found || job.Job.ID == observation.JobID
	}
	if rec.Code != 200 || len(d.JSON(preview.Candidate)) <= 12000 || !found || model.calls.Load() != 0 {
		t.Fatalf("radar was blocked by a model budget: status=%d candidate_bytes=%d model_calls=%d", rec.Code, len(d.JSON(preview.Candidate)), model.calls.Load())
	}
	rec = matchingRequest(handler, token, "/api/matching/analyze", "POST", map[string]any{"job_ids": []string{observation.JobID}, "candidate_hash": preview.CandidateHash})
	if rec.Code != 200 || model.calls.Load() != 2 {
		t.Fatalf("complete candidate could be previewed but not compared: status=%d model_calls=%d", rec.Code, model.calls.Load())
	}
}

func TestMatchingScreensBeyondSearchPageAndObservationsInvalidateByContent(t *testing.T) {
	ctx, s, _, u, _ := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{Degree: "MASTER", GraduationYear: 2027, Languages: []string{"Go"}, TargetRoles: []string{"后端开发"}}))
	wanted := map[string]bool{}
	var original p.Ingest
	var first d.Observation
	for i := 0; i < 600; i++ {
		in := p.Ingest{Company: "Synthetic Matching Catalog", Title: fmt.Sprintf("服务端开发 %d", i), JobType: "FULL_TIME", Locations: []string{"上海市"}, ExternalID: d.ID(), Text: "熟悉 Go", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()}
		o, err := s.IngestForUser(ctx, u, in)
		must(t, err)
		wanted[o.JobID] = true
		if i == 0 {
			original = in
			first = o
		}
	}
	model := matching.ModelIdentity("server-default", "fixture")
	snap, err := s.MatchSnapshot(ctx, u, model, "", nil)
	must(t, err)
	for _, row := range snap.Jobs {
		delete(wanted, row.Job.ID)
	}
	if len(wanted) != 0 {
		t.Fatal("catalog stopped at the search page limit", len(wanted))
	}
	before, err := s.MatchSnapshot(ctx, u, model, "", []string{first.JobID})
	must(t, err)
	original.ObservedAt = time.Now().UTC().Add(time.Second)
	_, err = s.IngestForUser(ctx, u, original)
	must(t, err)
	same, err := s.MatchSnapshot(ctx, u, model, "", []string{first.JobID})
	must(t, err)
	if before.Jobs[0].InputKey != same.Jobs[0].InputKey {
		t.Fatal("unchanged text required new paid work")
	}
	original.Text = "熟悉 Go 和 Redis"
	original.ObservedAt = original.ObservedAt.Add(time.Second)
	_, err = s.IngestForUser(ctx, u, original)
	must(t, err)
	changed, err := s.MatchSnapshot(ctx, u, model, "", []string{first.JobID})
	must(t, err)
	if before.Jobs[0].InputKey == changed.Jobs[0].InputKey {
		t.Fatal("changed requirements retained old match")
	}
	original.FetchStatus = "BLOCKED"
	original.Text = ""
	original.ObservedAt = original.ObservedAt.Add(time.Second)
	_, err = s.IngestForUser(ctx, u, original)
	must(t, err)
	blocked, err := s.MatchSnapshot(ctx, u, model, "", []string{first.JobID})
	must(t, err)
	if blocked.Jobs[0].TextBytes != 0 || blocked.Jobs[0].ExcludedReason == "" {
		t.Fatal("blocked latest observation analyzed historical text")
	}
}

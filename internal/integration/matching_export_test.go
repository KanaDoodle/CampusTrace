package integration

import (
	"encoding/json"
	"net/http/httptest"
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

func TestMatchingExportPrivateSanitizedOrderedAndWithoutModelCalls(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	profile := d.Profile{Languages: []string{"Go"}, PreferredCities: []string{"上海", "张小明"}}
	must(t, s.SaveProfile(ctx, u, profile))
	// Simulate legacy data from before sensitive identifiers were rejected on write.
	legacy, err := s.Profile(ctx, u)
	must(t, err)
	legacy.PreferredCities[1] = "联系人 张小明 private-city@example.com"
	_, err = s.DB.ExecContext(ctx, "UPDATE profiles SET body=? WHERE user_id=?", d.JSON(legacy), u)
	must(t, err)
	project, err := s.SaveProject(ctx, u, d.Project{Name: "private-project-name"})
	must(t, err)
	fact, err := s.SaveFact(ctx, u, d.ProjectFact{ProjectID: project.ID, Kind: "IMPLEMENTED", Verified: true, Claim: "张小明实现 Go 队列"})
	must(t, err)
	fact.Claim += "，邮箱 private-fact@example.com，电话 13812345678"
	fact.Reference = "https://private-project.invalid"
	_, err = s.DB.ExecContext(ctx, "UPDATE project_facts SET body=? WHERE id=? AND user_id=?", d.JSON(fact), fact.ID, u)
	must(t, err)
	_, err = s.SaveFact(ctx, u, d.ProjectFact{ProjectID: project.ID, Kind: "PLANNED", Verified: true, Claim: "private-planned-fact"})
	must(t, err)
	_, err = s.SaveFact(ctx, u, d.ProjectFact{ProjectID: project.ID, Kind: "IMPLEMENTED", Claim: "private-unverified-fact"})
	must(t, err)
	ids := []string{}
	var last p.Ingest
	for i := 0; i < 7; i++ {
		last = p.Ingest{Company: "联系人 张小明 private-company@example.com", Title: "Go backend " + d.ID(), Locations: []string{"上海", "private-location@example.com"}, JobType: "FULL_TIME", ExternalID: d.ID(), Text: "掌握 Go，张小明负责队列。联系 private-job@example.com 或 13812345678 https://private-job.invalid", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()}
		o, err := s.IngestForUser(ctx, u, last)
		must(t, err)
		ids = append(ids, o.JobID)
	}
	authn := auth.Service{Store: s, Secret: []byte("synthetic-matching-export-http-secret-32")}
	token, err := authn.Token(u)
	must(t, err)
	model := &matchFixtureModel{}
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New(), ResumeModel: model, ResumeModelName: "fixture"}).Handler()
	snap, err := s.MatchSnapshot(ctx, u, matching.ModelIdentity("server-default", "fixture"), "张小明", ids)
	must(t, err)
	// Preserve caller order, even when the database's initial ranking differs.
	for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
		ids[i], ids[j] = ids[j], ids[i]
	}
	body := map[string]any{"job_ids": ids, "candidate_hash": snap.CandidateHash, "mask_name": "张小明"}
	export := func(token string, body any) *httptest.ResponseRecorder {
		return matchingRequest(handler, token, "/api/matching/export", "POST", body)
	}
	rec := export(token, body)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	for _, private := range []string{u, "张小明", "private-city@example.com", "private-company@example.com", "private-fact@example.com", "private-job@example.com", "private-location@example.com", "13812345678", "private-project-name", "private-project.invalid", "private-job.invalid", "private-planned-fact", "private-unverified-fact"} {
		if strings.Contains(rec.Body.String(), private) {
			t.Fatal("export leaked private data", private)
		}
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("sensitive preview may be cached")
	}
	var out struct {
		Candidate   matching.Candidate  `json:"candidate"`
		Preferences map[string][]string `json:"preferences"`
		Jobs        []struct {
			ID string `json:"job_id"`
		} `json:"jobs"`
	}
	must(t, json.Unmarshal(rec.Body.Bytes(), &out))
	if len(out.Jobs) != len(ids) || len(out.Preferences["preferred_cities"]) != 2 {
		t.Fatal("export lost jobs or preferences")
	}
	for i, job := range out.Jobs {
		if job.ID != ids[i] {
			t.Fatal("export changed selected order")
		}
	}
	if model.calls.Load() != 0 {
		t.Fatal("manual export called model")
	}
	after, err := s.MatchSnapshot(ctx, u, "fixture", "张小明", ids)
	must(t, err)
	if after.CallsToday != snap.CallsToday {
		t.Fatal("manual export consumed matching quota")
	}
	other, err := s.NewUser(ctx, d.ID()+"@export-other.invalid", "unused")
	must(t, err)
	must(t, s.SaveProfile(ctx, other, d.Profile{Languages: []string{"Go"}}))
	otherToken, err := authn.Token(other)
	must(t, err)
	if rec = export(otherToken, body); rec.Code != 404 {
		t.Fatal("export leaked another user's private jobs", rec.Code)
	}
	if rec = export("", body); rec.Code != 401 {
		t.Fatal("export accepted unauthenticated request", rec.Code)
	}
	if rec = export(token, map[string]any{"job_ids": []string{ids[0], ids[0]}, "candidate_hash": snap.CandidateHash}); rec.Code != 400 {
		t.Fatal("export accepted duplicate IDs", rec.Code)
	}
	if rec = export(token, map[string]any{"job_ids": make([]string, 1001)}); rec.Code != 400 || !strings.Contains(rec.Body.String(), "MATCH_EXPORT_CAPACITY") {
		t.Fatal("export missing count limit", rec.Code)
	}
	profile.Skills = []string{"Redis"}
	must(t, s.SaveProfile(ctx, u, profile))
	if rec = export(token, body); rec.Code != 409 || !strings.Contains(rec.Body.String(), "MATCH_INPUT_CHANGED") {
		t.Fatal("export accepted stale candidate review", rec.Code)
	}
	next, err := s.MatchSnapshot(ctx, u, "fixture", "张小明", ids)
	must(t, err)
	body["candidate_hash"] = next.CandidateHash
	last.Text = ""
	last.FetchStatus = "BLOCKED"
	last.ObservedAt = time.Now().UTC().Add(time.Second)
	_, err = s.IngestForUser(ctx, u, last)
	must(t, err)
	if rec = export(token, body); rec.Code != 409 || !strings.Contains(rec.Body.String(), "MATCH_EXPORT_TEXT_REQUIRED") {
		t.Fatal("export reused old text after a failed latest fetch", rec.Code)
	}
}

func TestMatchingExportRejectsOversizedCatalogWithoutCallingModel(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{Languages: []string{"Go"}}))
	ids := []string{}
	for i := 0; i < 90; i++ {
		o, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Synthetic Large Export", Title: "Go backend " + d.ID(), JobType: "FULL_TIME", ExternalID: d.ID(), Text: strings.Repeat("G", 59900), FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
		must(t, err)
		ids = append(ids, o.JobID)
	}
	snap, err := s.MatchExportSnapshot(ctx, u, "fixture", "", ids)
	must(t, err)
	authn := auth.Service{Store: s, Secret: []byte("synthetic-matching-export-http-secret-32")}
	token, err := authn.Token(u)
	must(t, err)
	model := &matchFixtureModel{}
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New(), ResumeModel: model}).Handler()
	rec := matchingRequest(handler, token, "/api/matching/export", "POST", map[string]any{"job_ids": ids, "candidate_hash": snap.CandidateHash})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "MATCH_EXPORT_CAPACITY") || model.calls.Load() != 0 {
		t.Fatal("oversized export was not rejected locally", rec.Code, model.calls.Load())
	}
}

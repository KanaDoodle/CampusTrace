package integration

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
)

func TestBulkPreferencesAtomicOwnershipProtectionRestoreAndReimport(t *testing.T) {
	ctx, s, q, u, src := setup(t)
	profile := d.Profile{Degree: "MASTER", GraduationYear: 2027, TargetRoles: []string{"后端开发"}}
	must(t, s.SaveProfile(ctx, u, profile))
	other, err := s.NewUser(ctx, d.ID()+"@bulk-preferences.invalid", "unused")
	must(t, err)
	must(t, s.SaveProfile(ctx, other, profile))
	create := func(owner, title string) (p.Ingest, d.Observation) {
		in := p.Ingest{Company: "Synthetic direction cleanup", Title: title, JobType: "FULL_TIME", ExternalID: d.ID(), Text: "岗位职责\n负责会计核算\n任职要求\n本科及以上", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()}
		o, e := s.IngestForUser(ctx, owner, in)
		must(t, e)
		return in, o
	}
	in, one := create(u, "会计")
	_, two := create(u, "财务分析师")
	_, saved := create(u, "审计专员")
	_, planned := create(u, "机械工程师")
	_, foreign := create(other, "营销专员")
	must(t, s.SetPreference(ctx, u, saved.JobID, "SAVED"))
	_, err = s.ApplyAction(ctx, u, d.ID(), "create_application", []byte(d.JSON(p.CreateArgs{JobID: planned.JobID})))
	must(t, err)
	model := &matchFixtureModel{}
	authn := auth.Service{Store: s, Secret: []byte("synthetic-bulk-preferences-auth-secret")}
	token, err := authn.Token(u)
	must(t, err)
	otherToken, err := authn.Token(other)
	must(t, err)
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New(), ResumeModel: model, ResumeModelName: "fixture"}).Handler()
	request := func(ids []string, value, authToken string, status int) p.BulkPreferenceResult {
		t.Helper()
		r := matchingRequest(handler, authToken, "/api/jobs/preferences", "PUT", map[string]any{"job_ids": ids, "disposition": value})
		if r.Code != status {
			t.Fatalf("status=%d expected=%d body=%s", r.Code, status, r.Body.String())
		}
		var result p.BulkPreferenceResult
		if status == 200 {
			must(t, json.Unmarshal(r.Body.Bytes(), &result))
		}
		return result
	}
	request([]string{one.JobID, foreign.JobID}, "IGNORED", token, 404)
	before, err := s.MatchSnapshot(ctx, u, "fixture", "", []string{one.JobID})
	must(t, err)
	if before.Jobs[0].Disposition == "IGNORED" || before.Jobs[0].Local.Direction.Status != "UNRELATED" {
		t.Fatal("cross-owner batch partially applied or direction missing", before.Jobs)
	}
	request(nil, "IGNORED", token, 400)
	request([]string{one.JobID, one.JobID}, "IGNORED", token, 400)
	request([]string{"invalid"}, "IGNORED", token, 400)
	request([]string{one.JobID}, "SAVED", token, 400)
	request([]string{one.JobID}, "IGNORED", "", 401)
	tooMany := make([]string, p.MaxBulkPreferences+1)
	for i := range tooMany {
		tooMany[i] = d.ID()
	}
	request(tooMany, "IGNORED", token, 400)
	result := request([]string{one.JobID, two.JobID, saved.JobID, planned.JobID}, "IGNORED", token, 200)
	if len(result.Updated) != 2 || len(result.Skipped) != 2 {
		t.Fatal(result)
	}
	request([]string{one.JobID, two.JobID}, "IGNORED", token, 200)
	request([]string{one.JobID}, "NONE", otherToken, 404)
	// Periodic ingestion resolves the same posting identity and cannot undo the
	// per-user preference. Cleanup does not delete the source or job record.
	in.ObservedAt = in.ObservedAt.Add(time.Second)
	reimported, err := s.IngestForUser(ctx, u, in)
	must(t, err)
	if reimported.JobID != one.JobID {
		t.Fatal("fixture changed posting identity")
	}
	ignored, err := s.MatchSnapshot(ctx, u, "fixture", "", []string{one.JobID, saved.JobID, planned.JobID})
	must(t, err)
	for _, row := range ignored.Jobs {
		if row.Job.ID == one.JobID && (row.Disposition != "IGNORED" || !strings.Contains(row.ExcludedReason, "忽略")) {
			t.Fatal(row)
		}
		if row.Job.ID == saved.JobID && row.Disposition != "SAVED" || row.Job.ID == planned.JobID && (row.Disposition == "IGNORED" || row.Application == nil) {
			t.Fatal("bulk cleanup overwrote saved/application state", row)
		}
	}
	result = request([]string{one.JobID, two.JobID, saved.JobID}, "NONE", token, 200)
	if len(result.Updated) != 2 || len(result.Skipped) != 1 {
		t.Fatal(result)
	}
	restored, err := s.MatchSnapshot(ctx, u, "fixture", "", []string{one.JobID})
	must(t, err)
	if restored.Jobs[0].Disposition != "NONE" || restored.Jobs[0].Local.Direction.Status != "UNRELATED" || model.calls.Load() != 0 || restored.CallsToday != 0 {
		t.Fatal("restore changed direction, removed job or invoked model", restored)
	}
	// Reclassification uses current preferences, never a stale score.
	profile.TargetRoles = []string{"财务"}
	must(t, s.SaveProfile(ctx, u, profile))
	reclassified, err := s.MatchSnapshot(ctx, u, "fixture", "", []string{one.JobID})
	must(t, err)
	if reclassified.Jobs[0].Local.Direction.Status != "MATCH" {
		t.Fatal(reclassified.Jobs[0].Local)
	}
	shared, err := s.Ingest(ctx, ingest(t, ctx, s, src))
	must(t, err)
	request([]string{shared.JobID}, "IGNORED", token, 200)
	otherView, err := s.MatchSnapshot(ctx, other, "fixture", "", []string{shared.JobID})
	must(t, err)
	if otherView.Jobs[0].Disposition == "IGNORED" {
		t.Fatal("global job preference leaked to another user")
	}
}

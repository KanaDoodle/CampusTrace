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

func TestInventoryCampaignHidingTracksSubmissionsRulesAndAccountScope(t *testing.T) {
	ctx, s, _, u, source := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{Languages: []string{"Go"}, TargetRoles: []string{"后端开发"}}))
	company := "Quota fixture " + d.ID()
	ids := []string{}
	for _, title := range []string{"后端一", "后端二", "其他批次", "春招一", "春招二"} {
		o, err := s.Ingest(ctx, p.Ingest{SourceID: source, Company: company, Title: title, ExternalID: d.ID(), JobType: "FULL_TIME", Text: "岗位要求：熟悉Go。", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
		must(t, err)
		ids = append(ids, o.JobID)
	}
	job, err := p.One[d.Job](ctx, s.DB, "SELECT body FROM jobs WHERE id=?", ids[0])
	must(t, err)
	authn := auth.Service{Store: s, Secret: []byte("quota-fixture-at-least-32-byte-secret")}
	token, err := authn.Token(u)
	must(t, err)
	m := &wholeModel{}
	h := (&transport.API{Store: s, Auth: authn, ResumeModel: m, ResumeModelName: "whole-fixture"}).Handler()
	type response struct {
		Key   string `json:"snapshot_key"`
		Index struct {
			Full          bool `json:"full"`
			Fields        []string
			Rows, Upserts [][]any
		}
		Jobs []p.MatchJob
	}
	read := func(account, known string) response {
		t.Helper()
		w := matchingRequest(h, account, "/api/matching/inventory", "POST", map[string]any{"known_snapshot": known, "job_ids": ids})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var v response
		must(t, json.Unmarshal(w.Body.Bytes(), &v))
		return v
	}
	row := func(v response, id string) p.MatchJob {
		t.Helper()
		for _, r := range v.Jobs {
			if r.Job.ID == id {
				return r
			}
		}
		t.Fatal("missing card", id)
		return p.MatchJob{}
	}
	create := func(id string) d.Application {
		t.Helper()
		body, err := s.ApplyAction(ctx, u, d.ID(), "create_application", []byte(d.JSON(p.CreateArgs{JobID: id})))
		must(t, err)
		var a d.Application
		must(t, json.Unmarshal(body, &a))
		return a
	}
	initial := read(token, "")
	rule, err := s.SaveCampaign(ctx, u, "", p.ApplicationCampaign{CompanyID: job.CompanyID, Name: "秋招限投1个", Confirmed: true, Limit: 1, JobIDs: ids[:2]})
	must(t, err)
	_, err = s.SaveCampaign(ctx, u, "", p.ApplicationCampaign{CompanyID: job.CompanyID, Name: "独立春招", Confirmed: true, Limit: 1, JobIDs: ids[3:]})
	must(t, err)
	a := create(ids[0])
	create(ids[3])
	planned := read(token, initial.Key)
	if planned.Key == initial.Key || row(planned, ids[1]).Campaign.HideUnsubmitted || row(planned, ids[4]).Campaign.HideUnsubmitted {
		t.Fatal("rules did not invalidate cache, or reservations hid unsubmitted jobs")
	}
	views, err := s.Campaigns(ctx, u)
	must(t, err)
	if len(views) != 2 || views[0].Remaining != 0 || views[1].Remaining != 0 {
		t.Fatal("bulk usage differs from plan quota accounting", views)
	}
	_, err = s.ApplyAction(ctx, u, d.ID(), "transition_application", []byte(d.JSON(p.TransitionArgs{ApplicationID: a.ID, State: "APPLIED", Version: a.Version})))
	must(t, err)
	full := read(token, planned.Key)
	if full.Key == planned.Key || !row(full, ids[1]).Campaign.HideUnsubmitted || row(full, ids[0]).Campaign.HideUnsubmitted || row(full, ids[2]).Campaign != nil || row(full, ids[4]).Campaign.HideUnsubmitted {
		t.Fatal("full batch was not isolated from applied records and other batches")
	}
	if row(full, ids[1]).ExcludedReason != row(planned, ids[1]).ExcludedReason || row(full, ids[1]).InputKey != row(planned, ids[1]).InputKey || row(full, ids[1]).Disposition != row(planned, ids[1]).Disposition {
		t.Fatal("display hiding changed eligibility, analysis identity or preference")
	}
	field := -1
	for i, name := range full.Index.Fields {
		if name == "campaign" {
			field = i
		}
	}
	indexHasHint := false
	for _, values := range full.Index.Upserts {
		if values[0] == ids[1] && field >= 0 {
			v, ok := values[field].(map[string]any)
			indexHasHint = ok && v["hide_unsubmitted"] == true
		}
	}
	if full.Index.Full || !indexHasHint {
		t.Fatal("incremental index lost quota metadata")
	}
	identity := matching.ModelIdentity("server-default", "whole-fixture")
	subset, err := s.MatchSnapshot(ctx, u, identity, "", ids[1:2])
	must(t, err)
	if len(subset.Jobs) != 1 || subset.Jobs[0].Campaign.Submitted != 1 || !subset.Jobs[0].Campaign.HideUnsubmitted {
		t.Fatal("filtered snapshot counted only its visible applications")
	}
	w := matchingRequest(h, token, "/api/matching/company-candidates", "POST", map[string]any{"company": company})
	var candidates []p.MatchJob
	must(t, json.Unmarshal(w.Body.Bytes(), &candidates))
	if w.Code != 200 || !row(response{Jobs: candidates}, ids[1]).Campaign.HideUnsubmitted {
		t.Fatal("company picker lost quota hint", w.Body.String())
	}
	_, err = s.ApplyAction(ctx, u, d.ID(), "transition_application", []byte(d.JSON(p.TransitionArgs{ApplicationID: a.ID, State: "REJECTED", Version: a.Version + 1})))
	must(t, err)
	rejected := read(token, full.Key)
	if !row(rejected, ids[1]).Campaign.HideUnsubmitted || row(rejected, ids[0]).Campaign.HideUnsubmitted {
		t.Fatal("a real submission released quota after rejection")
	}
	rule.Limit = 2
	rule, err = s.SaveCampaign(ctx, u, rule.ID, rule)
	must(t, err)
	raised := read(token, rejected.Key)
	if raised.Key == rejected.Key || row(raised, ids[1]).Campaign.HideUnsubmitted {
		t.Fatal("raised quota did not restore browsing")
	}
	other, err := s.NewUser(ctx, d.ID()+"@quota-fixture.invalid", "unused")
	must(t, err)
	t.Cleanup(func() { cleanupFixture(ctx, s, other, "") })
	must(t, s.SaveProfile(ctx, other, d.Profile{Languages: []string{"Go"}}))
	otherToken, err := authn.Token(other)
	must(t, err)
	foreign := read(otherToken, raised.Key)
	if !foreign.Index.Full || len(foreign.Jobs) != len(ids) {
		t.Fatal("account reused another user's snapshot")
	}
	for _, r := range foreign.Jobs {
		if r.Campaign != nil || r.Application != nil {
			t.Fatal("personal quota or applications leaked to another account")
		}
	}
	must(t, s.DeleteCampaign(ctx, u, rule.ID, rule.Version))
	deleted := read(token, raised.Key)
	if deleted.Key == raised.Key || row(deleted, ids[1]).Campaign != nil || row(deleted, ids[0]).Application == nil || m.calls.Load() != 0 {
		t.Fatal("rule deletion failed to restore jobs, lost history or called the model")
	}
}

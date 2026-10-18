package integration

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
)

func TestApplicationFollowUpOwnedVersionedWorkflow(t *testing.T) {
	ctx, s, q, user, src := setup(t)
	o, err := s.Ingest(ctx, p.Ingest{SourceID: src, Company: "Follow-up fixture", Title: "Go 服务端", JobType: "FULL_TIME", URL: "https://follow.invalid/role", Text: jd, FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, err)
	raw, err := s.ApplyAction(ctx, user, d.ID(), "create_application", []byte(d.JSON(p.CreateArgs{JobID: o.JobID, Resume: "后端版"})))
	must(t, err)
	var original d.Application
	must(t, json.Unmarshal(raw, &original))
	authn := auth.Service{Store: s, Secret: []byte("follow-up-http-fixture-secret-32-characters")}
	token, err := authn.Token(user)
	must(t, err)
	h := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New()}).Handler()
	path := "/api/applications/" + original.ID + "/follow-up"
	at := time.Now().Add(time.Hour).In(time.FixedZone("west", -12*3600))
	in := p.ApplicationFollowUp{Version: 1, FollowUpAt: &at, NextAction: "  查看笔试通知  "}
	rec := matchingRequest(h, token, path, "PUT", in)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var app d.Application
	must(t, json.Unmarshal(rec.Body.Bytes(), &app))
	if app.Version != 2 || app.FollowUpAt == nil || !app.FollowUpAt.Equal(at) || app.FollowUpAt.Location() != time.UTC || app.NextAction != "查看笔试通知" || app.State != original.State || app.AppliedAt != nil || app.ResumeVersion != "后端版" || !app.CreatedAt.Equal(original.CreatedAt) {
		t.Fatal(app)
	}
	if rec = matchingRequest(h, token, path, "PUT", in); rec.Code != 409 {
		t.Fatal("stale reminder overwrote record", rec.Code)
	}
	other, err := s.NewUser(ctx, d.ID()+"@follow-other.invalid", "unused")
	must(t, err)
	otherToken, err := authn.Token(other)
	must(t, err)
	for _, method := range []string{"PUT", "POST"} {
		endpoint := path
		if method == "POST" {
			endpoint += "/complete"
		}
		if rec = matchingRequest(h, otherToken, endpoint, method, map[string]any{"version": 2}); rec.Code != 404 {
			t.Fatal("foreign reminder edit", rec.Code)
		}
		if rec = matchingRequest(h, "", endpoint, method, map[string]any{"version": 2}); rec.Code != 401 {
			t.Fatal("unauthenticated reminder edit", rec.Code)
		}
	}
	for _, invalid := range []map[string]any{{"version": 2, "next_action": "no date"}, {"version": 2, "follow_up_at": "bad"}, {"version": 2, "follow_up_at": "1999-01-01T00:00:00Z"}, {"version": 2, "follow_up_at": at, "next_action": strings.Repeat("字", 201)}} {
		if rec = matchingRequest(h, token, path, "PUT", invalid); rec.Code != 400 {
			t.Fatal("invalid reminder accepted", rec.Code, rec.Body.String())
		}
	}
	app, err = s.UpdateApplicationDetails(ctx, user, app.ID, p.ApplicationDetails{Version: 2, ResumeVersion: "新版", Note: "私密备注"})
	must(t, err)
	if app.FollowUpAt == nil || !app.FollowUpAt.Equal(at) || app.NextAction != "查看笔试通知" {
		t.Fatal("metadata edit cleared reminder", app)
	}
	raw, err = s.ApplyAction(ctx, user, d.ID(), "transition_application", []byte(d.JSON(p.TransitionArgs{ApplicationID: app.ID, State: "APPLIED", Version: app.Version})))
	must(t, err)
	must(t, json.Unmarshal(raw, &app))
	if app.FollowUpAt == nil || app.AppliedAt == nil {
		t.Fatal("progress lost reminder or applied timestamp", app)
	}
	applied := *app.AppliedAt
	// Two concurrent completions of the same version have one history event.
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- matchingRequest(h, token, path+"/complete", "POST", map[string]int{"version": app.Version}).Code
		}()
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatal("concurrent completions", counts)
	}
	row, err := s.ApplicationRecord(ctx, user, app.ID)
	must(t, err)
	if row.FollowUpAt != nil || row.NextAction != "" || row.State != "APPLIED" || row.AppliedAt == nil || !row.AppliedAt.Equal(applied) || row.Note != "私密备注" {
		t.Fatal("completion altered application", row)
	}
	history, err := s.ApplicationHistory(ctx, user, app.ID)
	must(t, err)
	completed := 0
	for _, event := range history {
		if event.Note == "已完成跟进：查看笔试通知" {
			completed++
		}
	}
	if completed != 1 {
		t.Fatal(history)
	}
	if rec = matchingRequest(h, token, path+"/complete", "POST", map[string]int{"version": row.Version}); rec.Code != 409 {
		t.Fatal("completed nonexistent reminder", rec.Code)
	}
	app, err = s.SaveApplicationFollowUp(ctx, user, app.ID, p.ApplicationFollowUp{Version: row.Version, FollowUpAt: &at})
	must(t, err)
	app, err = s.SaveApplicationFollowUp(ctx, user, app.ID, p.ApplicationFollowUp{Version: app.Version})
	must(t, err)
	if app.FollowUpAt != nil || app.NextAction != "" {
		t.Fatal("cancel left reminder", app)
	}
	app, err = s.SaveApplicationFollowUp(ctx, user, app.ID, p.ApplicationFollowUp{Version: app.Version, FollowUpAt: &at})
	must(t, err)
	_, err = s.ApplyAction(ctx, user, d.ID(), "transition_application", []byte(d.JSON(p.TransitionArgs{ApplicationID: app.ID, State: "WITHDRAWN", Version: app.Version})))
	must(t, err)
	row, err = s.ApplicationRecord(ctx, user, app.ID)
	must(t, err)
	if row.FollowUpAt != nil || row.NextAction != "" {
		t.Fatal("ended application retained reminder", row)
	}
	if rec = matchingRequest(h, token, path, "PUT", p.ApplicationFollowUp{Version: row.Version, FollowUpAt: &at}); rec.Code != 400 {
		t.Fatal("scheduled ended application", rec.Code)
	}
}

func TestApplicationFollowUpTodosBeijingDayOwnershipAndMatchingPrivacy(t *testing.T) {
	ctx, s, q, user, _ := setup(t)
	ids := taskJobs(t, ctx, s, user, 4)
	before, err := s.MatchSnapshot(ctx, user, matching.ModelIdentity("fixture", "fixture"), "", ids)
	must(t, err)
	zone := time.FixedZone("Asia/Shanghai", 8*3600)
	now := time.Now().In(zone)
	end := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, zone)
	times := []time.Time{now.Add(-48 * time.Hour), end.Add(-time.Second), end, now.Add(-time.Hour)}
	apps := []d.Application{}
	for i, id := range ids {
		raw, err := s.ApplyAction(ctx, user, d.ID(), "record_submitted_application", []byte(d.JSON(p.CreateArgs{JobID: id, Resume: "private-version"})))
		must(t, err)
		var app d.Application
		must(t, json.Unmarshal(raw, &app))
		app, err = s.SaveApplicationFollowUp(ctx, user, app.ID, p.ApplicationFollowUp{Version: 1, FollowUpAt: &times[i], NextAction: "follow-action"})
		must(t, err)
		apps = append(apps, app)
	}
	_, err = s.ApplyAction(ctx, user, d.ID(), "transition_application", []byte(d.JSON(p.TransitionArgs{ApplicationID: apps[3].ID, State: "WITHDRAWN", Version: apps[3].Version})))
	must(t, err)
	must(t, s.SetPreference(ctx, user, ids[0], "IGNORED"))
	_, err = s.DB.ExecContext(ctx, "UPDATE jobs SET body=JSON_SET(body,'$.current_status','CLOSED') WHERE id=?", ids[0])
	must(t, err)
	v, err := s.Todos(ctx, user)
	must(t, err)
	if v.Counts["APPLICATION_FOLLOW_UP"] != 2 || len(v.Items) != 2 || v.Items[0].ApplicationID != apps[0].ID || v.Items[1].ApplicationID != apps[1].ID || v.Items[0].NextAction != "follow-action" {
		t.Fatal("today boundary or applied/closed reminder lost", v)
	}
	other, err := s.NewUser(ctx, d.ID()+"@follow-todo-other.invalid", "unused")
	must(t, err)
	foreign, err := s.Todos(ctx, other)
	must(t, err)
	if len(foreign.Items) != 0 {
		t.Fatal("foreign reminder exposed", foreign)
	}
	_, err = s.DB.ExecContext(ctx, "UPDATE jobs SET visibility='PRIVATE',owner_id=? WHERE id=?", other, ids[0])
	must(t, err)
	v, err = s.Todos(ctx, user)
	must(t, err)
	if v.Items[0].Job != nil || v.Items[0].ApplicationID != apps[0].ID {
		t.Fatal("hidden job metadata exposed", v)
	}
	_, err = s.CompleteApplicationFollowUp(ctx, user, apps[0].ID, apps[0].Version)
	must(t, err)
	v, err = s.Todos(ctx, user)
	must(t, err)
	if v.Counts["APPLICATION_FOLLOW_UP"] != 1 || v.Items[0].ApplicationID != apps[1].ID {
		t.Fatal("completed reminder still due", v)
	}
	after, err := s.MatchSnapshot(ctx, user, matching.ModelIdentity("fixture", "fixture"), "", ids[1:2])
	must(t, err)
	beforeKey := ""
	for _, job := range before.Jobs {
		if job.Job.ID == ids[1] {
			beforeKey = job.InputKey
		}
	}
	if beforeKey == "" || after.CandidateHash != before.CandidateHash || after.Jobs[0].InputKey != beforeKey {
		t.Fatal("reminder changed model identity", after)
	}
	authn := auth.Service{Store: s, Secret: []byte("follow-up-privacy-fixture-secret-32-characters")}
	token, err := authn.Token(user)
	must(t, err)
	h := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New()}).Handler()
	preview := matchingRequest(h, token, "/api/matching/preview", "POST", map[string]any{})
	if preview.Code != 200 || strings.Contains(preview.Body.String(), "follow-action") || strings.Contains(preview.Body.String(), "private-version") {
		t.Fatal("workflow context leaked into model preview", preview.Code, preview.Body.String())
	}
	var calls int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM match_daily_usage WHERE user_id=?", user).Scan(&calls))
	if calls != 0 {
		t.Fatal("reminder used model quota", calls)
	}
}

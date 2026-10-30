package integration

import (
	"encoding/json"
	"errors"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestApplicationJourneyHTTPOptionalFieldsAndLargeNotice(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	o, e := s.IngestForUser(ctx, u, p.Ingest{Company: "合成HTTP公司", Title: "后端", JobType: "FULL_TIME", Text: "Go 后端开发", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, e)
	raw, e := s.ApplyAction(ctx, u, d.ID(), "record_submitted_application", []byte(d.JSON(p.CreateArgs{JobID: o.JobID})))
	must(t, e)
	var a d.Application
	must(t, json.Unmarshal(raw, &a))
	authn := auth.Service{Store: s, Secret: []byte("synthetic-http-journey-secret-32-bytes")}
	token, e := authn.Token(u)
	must(t, e)
	h := (&transport.API{Store: s, Queue: q, Auth: authn}).Handler()
	content := "合成HTTP公司通知\n" + strings.Repeat("a", 70000)
	rec := matchingRequest(h, token, "/api/recruitment-notices", "POST", map[string]any{"content": content, "format": "text"})
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var view p.NoticeView
	must(t, json.Unmarshal(rec.Body.Bytes(), &view))
	body := map[string]any{"version": 1, "application_id": a.ID, "application_version": 1, "kind": "UNKNOWN", "round": 1, "reference": "", "note": "仅关联，无日期"}
	rec = matchingRequest(h, token, "/api/recruitment-notices/"+view.Notice.ID+"/confirm", "POST", body)
	if rec.Code != 200 {
		t.Fatal("omitted optional fields rejected", rec.Code, rec.Body.String())
	}
	rec = matchingRequest(h, token, "/api/recruitment-notices/"+view.Notice.ID+"/confirm", "POST", body)
	if rec.Code != 200 {
		t.Fatal("HTTP replay", rec.Code)
	}
	body["scheduled_at"] = nil
	rec = matchingRequest(h, token, "/api/recruitment-notices/"+view.Notice.ID+"/confirm", "POST", body)
	if rec.Code != 400 {
		t.Fatal("null contract silently changed", rec.Code)
	}
}

func TestApplicationJourneyAtomicPlanSnapshotsNoticesAndRescheduling(t *testing.T) {
	ctx, s, _, u, _ := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{Languages: []string{"Go"}, TargetRoles: []string{"后端开发"}, Degree: "MASTER", GraduationYear: 2027}))
	ids := []string{}
	for _, title := range []string{"Go 后端开发", "平台后端开发", "基础架构开发"} {
		o, e := s.IngestForUser(ctx, u, p.Ingest{Company: "合成青杉科技", Title: title, ExternalID: d.ID(), JobType: "FULL_TIME", Text: "熟悉 Go，负责后端开发和可靠性。", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
		must(t, e)
		ids = append(ids, o.JobID)
	}
	job, e := s.Job(ctx, ids[0])
	must(t, e)
	_, e = s.SaveCampaign(ctx, u, "", p.ApplicationCampaign{CompanyID: job.CompanyID, Name: "合成2027秋招", Limit: 2, JobIDs: ids, Confirmed: true})
	must(t, e)
	snapshot, e := s.MatchWorkspaceSnapshot(ctx, u, "offline", "", ids, job.Company)
	must(t, e)
	report := matching.CompanyComparison{Holistic: &matching.HolisticCompanyReport{Choices: []matching.CompanyChoice{{ID: ids[0], Rank: 1}, {ID: ids[1], Rank: 2}, {ID: ids[2], Rank: 3}}}}
	plan := p.BuildCompanyDecisionPlan(snapshot, report)
	if len(plan.Suggested) != 2 {
		t.Fatal(plan)
	}
	if _, e = s.ConfirmCompanyDecision(ctx, u, "offline", "", job.Company, ids, ids, plan.Key); !errors.Is(e, p.ErrCampaignLimit) {
		t.Fatal("overquota batch did not roll back", e)
	}
	rows, e := s.ApplicationRecords(ctx, u)
	must(t, e)
	if len(rows) != 0 {
		t.Fatal("partial batch committed")
	}
	apps, e := s.ConfirmCompanyDecision(ctx, u, "offline", "", job.Company, ids, plan.Suggested, plan.Key)
	must(t, e)
	again, e := s.ConfirmCompanyDecision(ctx, u, "offline", "", job.Company, ids, plan.Suggested, plan.Key)
	must(t, e)
	if d.JSON(apps) != d.JSON(again) {
		t.Fatal("batch replay changed result")
	}
	a := apps[0]
	preview, e := s.ApplicationContextPreview(ctx, u, a.ID, "合成后端简历正文")
	must(t, e)
	must(t, s.SaveProfile(ctx, u, d.Profile{Languages: []string{"Go", "Java"}, TargetRoles: []string{"后端开发"}}))
	_, e = s.ApplyAction(ctx, u, d.ID(), "transition_application", []byte(d.JSON(p.TransitionArgs{ApplicationID: a.ID, State: "APPLIED", Version: 1, ContextKey: preview.Key, ResumeText: "合成后端简历正文"})))
	if !errors.Is(e, p.ErrConflict) {
		t.Fatal("stale preview accepted", e)
	}
	preview, e = s.ApplicationContextPreview(ctx, u, a.ID, "合成后端简历正文")
	must(t, e)
	raw, e := s.ApplyAction(ctx, u, d.ID(), "transition_application", []byte(d.JSON(p.TransitionArgs{ApplicationID: a.ID, State: "APPLIED", Version: 1, ContextKey: preview.Key, ResumeText: "合成后端简历正文"})))
	must(t, e)
	must(t, json.Unmarshal(raw, &a))
	second := apps[1]
	_, e = s.ApplyAction(ctx, u, d.ID(), "transition_application", []byte(d.JSON(p.TransitionArgs{ApplicationID: second.ID, State: "APPLIED", Version: 1})))
	must(t, e)
	original, e := s.ApplicationDossier(ctx, u, a.ID)
	must(t, e)
	if original.Context == nil || original.Context.Phase != "SUBMITTED" || !original.Context.Reviewed || len(original.Contexts) != 2 {
		t.Fatal(original)
	}
	must(t, s.SaveProfile(ctx, u, d.Profile{Languages: []string{"Python"}}))
	after, e := s.ApplicationDossier(ctx, u, a.ID)
	must(t, e)
	if d.JSON(after.Context) != d.JSON(original.Context) {
		t.Fatal("snapshot changed with live profile")
	}
	text := "合成青杉科技面试通知\n请于 2026-11-02 14:00 参加面试。"
	n, e := s.ImportRecruitmentNotice(ctx, u, text, "text")
	must(t, e)
	if len(n.Candidates) != 2 || !n.Ambiguous {
		t.Fatal("ambiguity hidden", n)
	}
	n2, e := s.ImportRecruitmentNotice(ctx, u, text, "text")
	must(t, e)
	if n2.Notice.ID != n.Notice.ID {
		t.Fatal("duplicate import")
	}
	in := p.NoticeConfirmation{Version: 1, ApplicationID: a.ID, ApplicationVersion: a.Version, Kind: "INTERVIEW", ScheduledAt: n.Notice.Parsed.ScheduledAt, Round: 1, Reference: "SYN-101"}
	bad := in
	bad.ApplicationVersion = 1
	if _, e = s.ConfirmRecruitmentNotice(ctx, u, n.Notice.ID, bad); !errors.Is(e, p.ErrConflict) {
		t.Fatal("stale confirmation", e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.ConfirmRecruitmentNotice(ctx, u, n.Notice.ID, in); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		must(t, e)
	}
	dossier, e := s.ApplicationDossier(ctx, u, a.ID)
	must(t, e)
	if len(dossier.Interviews) != 1 || len(dossier.Notices) != 1 || dossier.Application.State != "INTERVIEW" {
		t.Fatal("duplicate confirmation", dossier)
	}
	old := dossier.Interviews[0]
	change, e := s.ImportRecruitmentNotice(ctx, u, "合成青杉科技面试改期：原时间 2026-11-02 14:00，新时间 2026-11-03 16:00。", "text")
	must(t, e)
	at := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	reschedule := p.NoticeConfirmation{Version: 1, ApplicationID: a.ID, ApplicationVersion: dossier.Application.Version, Kind: "RESCHEDULE", ScheduledAt: &at, Round: 1, InterviewID: old.ID, PreviousScheduledAt: &old.ScheduledAt}
	_, e = s.ConfirmRecruitmentNotice(ctx, u, change.Notice.ID, reschedule)
	must(t, e)
	_, e = s.ConfirmRecruitmentNotice(ctx, u, change.Notice.ID, reschedule)
	must(t, e)
	dossier, e = s.ApplicationDossier(ctx, u, a.ID)
	must(t, e)
	if len(dossier.Interviews) != 1 || !dossier.Interviews[0].ScheduledAt.Equal(at) || len(dossier.Notices) != 2 {
		t.Fatal("reschedule created a second interview", dossier)
	}
	other, e := s.NewUser(ctx, d.ID()+"@journey.invalid", "unused")
	must(t, e)
	t.Cleanup(func() { cleanupFixture(ctx, s, other, "") })
	if _, e = s.ApplicationDossier(ctx, other, a.ID); !errors.Is(e, p.ErrNotFound) {
		t.Fatal("cross-owner dossier", e)
	}
	if _, e = s.RecruitmentNotice(ctx, other, n.Notice.ID); !errors.Is(e, p.ErrNotFound) {
		t.Fatal("cross-owner notice", e)
	}
	// Legacy rows are never silently filled with today's information.
	_, e = s.DB.ExecContext(ctx, "DELETE FROM application_contexts WHERE application_id=?", second.ID)
	must(t, e)
	legacy, e := s.ApplicationDossier(ctx, u, second.ID)
	must(t, e)
	if !legacy.Legacy || legacy.Context != nil {
		t.Fatal("fabricated legacy snapshot")
	}
}

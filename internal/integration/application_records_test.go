package integration

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
)

func TestApplicationRecordsJoinOwnedJobsAndVersionDetailsWithoutChangingStage(t *testing.T) {
	ctx, s, q, u, source := setup(t)
	o, err := s.Ingest(ctx, p.Ingest{SourceID: source, Company: "Application fixture", Title: "服务端开发", JobType: "FULL_TIME", URL: "https://careers.example.invalid/campus/role", Text: jd, FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, err)
	raw, err := s.ApplyAction(ctx, u, d.ID(), "create_application", []byte(d.JSON(p.CreateArgs{JobID: o.JobID, Resume: "旧版本"})))
	must(t, err)
	var original d.Application
	must(t, json.Unmarshal(raw, &original))
	authn := auth.Service{Store: s, Secret: []byte("application-records-test-secret-32")}
	token, err := authn.Token(u)
	must(t, err)
	h := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New()}).Handler()
	list := func() []p.ApplicationRecord {
		t.Helper()
		rec := matchingRequest(h, token, "/api/applications", "GET", nil)
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(rec.Code, rec.Body.String())
		}
		var rows []p.ApplicationRecord
		must(t, json.Unmarshal(rec.Body.Bytes(), &rows))
		return rows
	}
	rows := list()
	if len(rows) != 1 || rows[0].Job == nil || rows[0].Job.Company != "Application fixture" || rows[0].Job.Title != "服务端开发" || rows[0].OfficialURL != "https://careers.example.invalid/campus/role" || len(rows[0].NextStates) != 2 {
		t.Fatal(rows)
	}
	body := p.ApplicationDetails{Version: original.Version, ResumeVersion: "后端版 9月29日", Note: "等待笔试通知"}
	rec := matchingRequest(h, token, "/api/applications/"+original.ID, "PUT", body)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	updated := list()[0]
	if updated.State != "PLANNED" || updated.AppliedAt != nil || updated.Version != 2 || updated.Note != body.Note || updated.ResumeVersion != body.ResumeVersion || !updated.CreatedAt.Equal(original.CreatedAt) {
		t.Fatal(updated)
	}
	rec = matchingRequest(h, token, "/api/applications/"+original.ID, "PUT", body)
	if rec.Code != 409 {
		t.Fatal("stale edit overwrote details", rec.Code)
	}
	rec = matchingRequest(h, token, "/api/applications/transition", "POST", p.TransitionArgs{ApplicationID: original.ID, State: "APPLIED", Version: 2, Note: "官网提交完成"})
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	applied := list()[0]
	if applied.AppliedAt == nil || applied.Note != body.Note || applied.ResumeVersion != body.ResumeVersion {
		t.Fatal(applied)
	}
	when := *applied.AppliedAt
	_, err = s.UpdateApplicationDetails(ctx, u, original.ID, p.ApplicationDetails{Version: 3, ResumeVersion: "后端终版", Note: "已收到笔试"})
	must(t, err)
	if current := list()[0]; current.AppliedAt == nil || !current.AppliedAt.Equal(when) || current.State != "APPLIED" {
		t.Fatal("metadata edit rewrote applied time", current)
	}
	history, err := s.ApplicationHistory(ctx, u, original.ID)
	must(t, err)
	if len(history) != 4 || history[2].Note != "官网提交完成" {
		t.Fatal(history)
	}
	other, err := s.NewUser(ctx, d.ID()+"@application-other.invalid", "unused")
	must(t, err)
	otherToken, err := authn.Token(other)
	must(t, err)
	rec = matchingRequest(h, otherToken, "/api/applications/"+original.ID, "PUT", p.ApplicationDetails{Version: 4, ResumeVersion: "forged"})
	if rec.Code != 404 {
		t.Fatal("cross-owner edit", rec.Code)
	}
	foreign, err := s.ApplicationRecords(ctx, other)
	must(t, err)
	if len(foreign) != 0 {
		t.Fatal("private records leaked")
	}
	if _, err = s.UpdateApplicationDetails(ctx, u, original.ID, p.ApplicationDetails{Version: 4, Note: string(make([]byte, 2001))}); !errors.Is(err, p.ErrValidation) {
		t.Fatal(err)
	}
	// Legacy records can outlive visibility changes; never expose a foreign
	// private source or job merely because this user owns an application.
	_, err = s.DB.ExecContext(ctx, "UPDATE sources SET body=? WHERE id=?", d.JSON(d.Source{ID: source, Trust: "OFFICIAL", Visibility: "PRIVATE", OwnerID: other}), source)
	must(t, err)
	if row := list()[0]; row.Job == nil || row.OfficialURL != "" {
		t.Fatal("foreign private source exposed", row)
	}
	_, err = s.DB.ExecContext(ctx, "UPDATE jobs SET visibility='PRIVATE',owner_id=? WHERE id=?", other, o.JobID)
	must(t, err)
	if row := list()[0]; row.Job != nil || row.OfficialURL != "" {
		t.Fatal("foreign private job exposed", row)
	}
}

func TestApplicationRecordsNeverLabelManualOrUnsafeLinksAsOfficial(t *testing.T) {
	ctx, s, _, u, source := setup(t)
	for _, link := range []string{"javascript:alert(1)", "https://secret:password@careers.example.invalid/role"} {
		o, err := s.Ingest(ctx, p.Ingest{SourceID: source, Company: "Unsafe fixture", Title: "开发岗位", JobType: "FULL_TIME", ExternalID: d.ID(), URL: link, Text: jd, FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
		must(t, err)
		_, err = s.ApplyAction(ctx, u, d.ID(), "create_application", []byte(d.JSON(p.CreateArgs{JobID: o.JobID})))
		must(t, err)
	}
	o, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Manual fixture", Title: "开发岗位", JobType: "FULL_TIME", URL: "https://manual.example.invalid/role", Text: jd, FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, err)
	_, err = s.ApplyAction(ctx, u, d.ID(), "create_application", []byte(d.JSON(p.CreateArgs{JobID: o.JobID})))
	must(t, err)
	rows, err := s.ApplicationRecords(ctx, u)
	must(t, err)
	if len(rows) != 3 {
		t.Fatal(rows)
	}
	for _, row := range rows {
		if row.OfficialURL != "" {
			t.Fatal("unsafe/manual URL labelled official", row.OfficialURL)
		}
	}
}

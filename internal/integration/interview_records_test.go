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

func TestInterviewRecordsPreserveCompletionTimeAndOwnedContext(t *testing.T) {
	ctx, s, q, user, source := setup(t)
	o, err := s.Ingest(ctx, p.Ingest{SourceID: source, Company: "Interview fixture", Title: "Go 后端开发", JobType: "FULL_TIME", Text: jd, FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, err)
	raw, err := s.ApplyAction(ctx, user, d.ID(), "create_application", []byte(d.JSON(p.CreateArgs{JobID: o.JobID})))
	must(t, err)
	var app d.Application
	must(t, json.Unmarshal(raw, &app))
	interview, err := s.SaveInterview(ctx, user, d.Interview{ApplicationID: app.ID, Round: 1, ScheduledAt: time.Now().Add(time.Hour)})
	must(t, err)
	if interview.Result != "PENDING" {
		t.Fatal(interview)
	}
	authn := auth.Service{Store: s, Secret: []byte("interview-records-test-secret-32-chars")}
	token, err := authn.Token(user)
	must(t, err)
	h := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New()}).Handler()
	list := func(token string) []p.InterviewRecord {
		t.Helper()
		rec := matchingRequest(h, token, "/api/interviews", "GET", nil)
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(rec.Code, rec.Body.String())
		}
		var rows []p.InterviewRecord
		must(t, json.Unmarshal(rec.Body.Bytes(), &rows))
		return rows
	}
	rows := list(token)
	if len(rows) != 1 || rows[0].Job == nil || rows[0].Job.Company != "Interview fixture" || rows[0].Review != nil {
		t.Fatal(rows)
	}
	rec := matchingRequest(h, token, "/api/interviews/"+interview.ID+"/finish", "POST", map[string]any{"result": "PENDING", "notes": "本轮完成，等待反馈"})
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	first := list(token)[0]
	if first.FinishedAt == nil || first.Result != "PENDING" {
		t.Fatal(first)
	}
	review := d.Review{InterviewID: interview.ID, Questions: []string{"事务如何隔离？"}, Evaluation: "需要补充行锁细节", Missed: []string{}, Topics: []d.WeakCandidate{}}
	_, err = s.ApplyAction(ctx, user, d.ID(), "record_interview_review", []byte(d.JSON(review)))
	must(t, err)
	rec = matchingRequest(h, token, "/api/interviews/"+interview.ID+"/finish", "POST", map[string]any{"result": "PASS", "notes": "收到通过通知"})
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	current := list(token)[0]
	if current.FinishedAt == nil || !current.FinishedAt.Equal(*first.FinishedAt) || current.Result != "PASS" || current.Review == nil || current.Review.Evaluation != review.Evaluation {
		t.Fatal(current)
	}
	applications, err := s.ApplicationRecords(ctx, user)
	must(t, err)
	if applications[0].State != "PLANNED" {
		t.Fatal("interview feedback changed application stage")
	}
	rec = matchingRequest(h, token, "/api/interviews/"+interview.ID+"/finish", "POST", map[string]any{"result": "FAIL", "notes": strings.Repeat("x", 8001)})
	if rec.Code != 400 {
		t.Fatal("oversized notes", rec.Code)
	}
	other, err := s.NewUser(ctx, d.ID()+"@interview-other.invalid", "unused")
	must(t, err)
	otherToken, err := authn.Token(other)
	must(t, err)
	if len(list(otherToken)) != 0 {
		t.Fatal("foreign interviews leaked")
	}
	rec = matchingRequest(h, otherToken, "/api/interviews/"+interview.ID+"/finish", "POST", map[string]any{"result": "FAIL", "notes": "forged"})
	if rec.Code != 404 {
		t.Fatal("cross-owner result write", rec.Code)
	}
	_, err = s.DB.ExecContext(ctx, "UPDATE jobs SET visibility='PRIVATE',owner_id=? WHERE id=?", other, o.JobID)
	must(t, err)
	private := list(token)[0]
	if private.Job != nil || private.Review == nil {
		t.Fatal("foreign private job exposed or owned review lost", private)
	}
	_, err = s.DB.ExecContext(ctx, "UPDATE reviews SET user_id=? WHERE interview_id=?", other, interview.ID)
	must(t, err)
	if list(token)[0].Review != nil {
		t.Fatal("foreign review exposed")
	}
}

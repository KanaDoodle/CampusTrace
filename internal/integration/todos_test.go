package integration

import (
	"encoding/json"
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

func TestTodosOwnedWorkflowAndReadOnlyProjection(t *testing.T) {
	ctx, s, q, user, src := radarSetup(t)
	in := ingest(t, ctx, s, src)
	in.Text = jd + "\ndeadline: " + time.Now().Add(48*time.Hour).UTC().Format(time.RFC3339)
	o, err := s.Ingest(ctx, in)
	must(t, err)
	must(t, worker(s, q).Process(ctx, p.NewTask("ANALYZE", o.ID)))
	must(t, s.Assess(ctx, d.ID(), o.JobID))
	must(t, s.SaveProfile(ctx, user, d.Profile{GraduationYear: 2027, Degree: "MASTER", Languages: []string{"Go"}}))
	before, err := s.MatchSnapshot(ctx, user, matching.ModelIdentity("fixture", "fixture"), "", []string{o.JobID})
	must(t, err)
	raw, err := s.ApplyAction(ctx, user, d.ID(), "create_application", []byte(d.JSON(p.CreateArgs{JobID: o.JobID})))
	must(t, err)
	var app d.Application
	must(t, json.Unmarshal(raw, &app))
	app, err = s.UpdateApplicationDetails(ctx, user, app.ID, p.ApplicationDetails{Version: app.Version, Note: "private-application-note", ResumeVersion: "private-resume-version"})
	must(t, err)
	future, err := s.SaveInterview(ctx, user, d.Interview{ApplicationID: app.ID, Round: 1, ScheduledAt: time.Now().Add(2 * time.Hour).In(time.FixedZone("west", -12*3600)), Notes: "private-interview-notes"})
	must(t, err)
	done, err := s.SaveInterview(ctx, user, d.Interview{ApplicationID: app.ID, Round: 2, ScheduledAt: time.Now().Add(-time.Hour)})
	must(t, err)
	_, err = s.FinishInterview(ctx, user, done.ID, "PENDING", "private-finish-notes")
	must(t, err)
	authn := auth.Service{Store: s, Secret: []byte("todo-http-fixture-secret-32-characters")}
	token, err := authn.Token(user)
	must(t, err)
	h := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New()}).Handler()
	read := func(token string) p.TodoSnapshot {
		t.Helper()
		r := matchingRequest(h, token, "/api/radar/todos", "GET", nil)
		if r.Code != 200 || r.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(r.Code, r.Body.String())
		}
		for _, value := range []string{"private-application-note", "private-resume-version", "private-interview-notes", "private-finish-notes"} {
			if strings.Contains(r.Body.String(), value) {
				t.Fatal("private detail leaked into summary", value)
			}
		}
		var v p.TodoSnapshot
		must(t, json.Unmarshal(r.Body.Bytes(), &v))
		return v
	}
	v := read(token)
	if v.Counts["PLANNED_CLOSING"] != 1 || v.Counts["INTERVIEW_UPCOMING"] != 1 || v.Counts["REVIEW_PENDING"] != 1 {
		t.Fatal(v)
	}
	for _, item := range v.Items {
		if item.Job == nil || item.Job.ID != o.JobID {
			t.Fatal(item)
		}
		if item.Kind == "INTERVIEW_UPCOMING" && item.InterviewID != future.ID {
			t.Fatal(item)
		}
	}
	after, err := s.MatchSnapshot(ctx, user, matching.ModelIdentity("fixture", "fixture"), "", []string{o.JobID})
	must(t, err)
	if after.CandidateHash != before.CandidateHash || after.Jobs[0].InputKey != before.Jobs[0].InputKey || after.Jobs[0].Application == nil || after.Jobs[0].Application.State != "PLANNED" {
		t.Fatal("workflow changed matching identity", after)
	}
	preview := matchingRequest(h, token, "/api/matching/preview", "POST", map[string]any{})
	if preview.Code != 200 || strings.Contains(preview.Body.String(), "private-application-note") || strings.Contains(preview.Body.String(), "private-resume-version") {
		t.Fatal(preview.Code, preview.Body.String())
	}
	other, err := s.NewUser(ctx, d.ID()+"@todo-foreign.invalid", "unused")
	must(t, err)
	otherToken, err := authn.Token(other)
	must(t, err)
	if len(read(otherToken).Items) != 0 {
		t.Fatal("foreign todos exposed")
	}
	for _, path := range []string{"/api/interviews/" + future.ID, "/api/applications/" + app.ID} {
		if rec := matchingRequest(h, otherToken, path, "GET", nil); rec.Code != 404 {
			t.Fatal(path, rec.Code)
		}
		if rec := matchingRequest(h, token, path, "GET", nil); rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(path, rec.Code)
		}
	}
	if rec := matchingRequest(h, "", "/api/radar/todos", "GET", nil); rec.Code != 401 {
		t.Fatal("unauthenticated todo read", rec.Code)
	}
	_, err = s.ApplyAction(ctx, user, d.ID(), "transition_application", []byte(d.JSON(p.TransitionArgs{ApplicationID: app.ID, State: "APPLIED", Version: app.Version})))
	must(t, err)
	if read(token).Counts["PLANNED_CLOSING"] != 0 {
		t.Fatal("already applied plan still due")
	}
	after, err = s.MatchSnapshot(ctx, user, matching.ModelIdentity("fixture", "fixture"), "", []string{o.JobID})
	must(t, err)
	if after.Jobs[0].Application.AppliedAt == nil || after.Jobs[0].Application.State != "APPLIED" || after.Jobs[0].InputKey != before.Jobs[0].InputKey {
		t.Fatal(after)
	}
	review := d.Review{InterviewID: done.ID, Questions: []string{"事务如何隔离？"}, Evaluation: "需要复习锁", Missed: []string{}, Topics: []d.WeakCandidate{}}
	_, err = s.ApplyAction(ctx, user, d.ID(), "record_interview_review", []byte(d.JSON(review)))
	must(t, err)
	if read(token).Counts["REVIEW_PENDING"] != 0 {
		t.Fatal("saved review still pending")
	}
	_, err = s.DB.ExecContext(ctx, "UPDATE jobs SET visibility='PRIVATE',owner_id=? WHERE id=?", other, o.JobID)
	must(t, err)
	v = read(token)
	for _, item := range v.Items {
		if item.Job != nil {
			t.Fatal("foreign private metadata exposed", item)
		}
	}
	var calls int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM match_daily_usage WHERE user_id=?", user).Scan(&calls))
	if calls != 0 {
		t.Fatal("todo reads used paid quota")
	}
}

func TestTodoFailuresDeduplicateLatestAttemptsAndKeepReadOnly(t *testing.T) {
	ctx, s, _, user, _ := setup(t)
	ids := taskJobs(t, ctx, s, user, 4)
	insert := func(state string, at time.Time, items []p.MatchRunItem) p.MatchRun {
		t.Helper()
		run := p.MatchRun{ID: d.ID(), State: state, CreatedAt: at, UpdatedAt: at, Items: items, Version: 1, RequestKey: d.ID()}
		_, err := s.DB.ExecContext(ctx, "INSERT INTO match_runs(id,user_id,request_key,state,updated_at,body) VALUES(?,?,?,?,?,?)", run.ID, user, run.RequestKey, run.State, run.UpdatedAt, d.JSON(run))
		must(t, err)
		return run
	}
	old := insert("COMPLETED_WITH_ERRORS", time.Now().Add(-time.Hour).UTC(), []p.MatchRunItem{{JobID: ids[0], State: "FAILED"}, {JobID: ids[1], State: "FAILED"}, {JobID: ids[2], State: "FAILED"}, {JobID: ids[3], State: "FAILED"}})
	insert("COMPLETED", time.Now().Add(-30*time.Minute).UTC(), []p.MatchRunItem{{JobID: ids[1], State: "SUCCEEDED"}})
	insert("CANCELLED", time.Now().Add(-20*time.Minute).UTC(), []p.MatchRunItem{{JobID: ids[2], State: "FAILED"}})
	_, err := s.DB.ExecContext(ctx, "INSERT INTO job_match_results(user_id,job_id,body) VALUES(?,?,?)", user, ids[3], d.JSON(map[string]any{"analyzed_at": time.Now().UTC()}))
	must(t, err)
	v, err := s.Todos(ctx, user)
	must(t, err)
	if v.Counts["ANALYSIS_FAILED"] != 1 || len(v.Items) != 1 || v.Items[0].TaskID != old.ID || v.Items[0].Job.ID != ids[0] {
		t.Fatal(v)
	}
	must(t, s.SetPreference(ctx, user, ids[0], "IGNORED"))
	v, err = s.Todos(ctx, user)
	must(t, err)
	if v.Counts["ANALYSIS_FAILED"] != 0 {
		t.Fatal(v)
	}
	current, err := s.MatchRun(ctx, user, old.ID)
	must(t, err)
	if current.Version != old.Version || current.State != old.State {
		t.Fatal("read changed task state")
	}
}

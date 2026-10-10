package integration

import (
	"database/sql"
	"sync"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func TestAssessmentScheduleCoalescesChangesAndSkipsUnchangedHistory(t *testing.T) {
	ctx, s, q, u, source := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{Degree: "MASTER", GraduationYear: 2027, Skills: []string{"Go"}}))
	in := ingest(t, ctx, s, source)
	o, err := s.Ingest(ctx, in)
	must(t, err)
	w := worker(s, q)
	must(t, w.Process(ctx, p.NewTask("ANALYZE", o.ID)))
	var task string
	must(t, s.DB.QueryRowContext(ctx, "SELECT pending_task_id FROM job_assessment_schedule WHERE job_id=?", o.JobID).Scan(&task))
	var queued int
	must(t, s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.task_type'))='ASSESS' AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.entity_id'))=?`, o.JobID).Scan(&queued))
	if queued != 1 {
		t.Fatal("ingest, binding and analysis did not coalesce", queued)
	}
	must(t, s.Assess(ctx, task, o.JobID))
	var pending sql.NullString
	var due sql.NullTime
	must(t, s.DB.QueryRowContext(ctx, "SELECT pending_task_id,next_assess_at FROM job_assessment_schedule WHERE job_id=?", o.JobID).Scan(&pending, &due))
	if pending.Valid || !due.Valid || due.Time.Before(time.Now().Add(6*24*time.Hour)) {
		t.Fatal("assessment did not release the task and schedule its freshness boundary", pending, due)
	}
	must(t, s.Assess(ctx, d.ID(), o.JobID))
	var histories int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM assessments WHERE job_id=?", o.JobID).Scan(&histories))
	if histories != 1 {
		t.Fatal("unchanged input appended history", histories)
	}
	// Profile updates stay accurate on read without queuing every job again.
	profile, err := s.Profile(ctx, u)
	must(t, err)
	profile.Degree = "ASSOCIATE"
	must(t, s.SaveProfile(ctx, u, profile))
	evaluation, err := s.QueryEvaluation(ctx, u, o.JobID)
	must(t, err)
	if evaluation.Eligibility.Status != "INELIGIBLE" {
		t.Fatal("profile change required hourly background work", evaluation.Eligibility.Status)
	}
	// A subsequent fetch must generate fresh work even when a prior task exists.
	in.ObservedAt = time.Now().UTC()
	in.FetchStatus, in.Text = "TIMEOUT", ""
	newObservation, err := s.Ingest(ctx, in)
	must(t, err)
	must(t, s.DB.QueryRowContext(ctx, "SELECT pending_task_id FROM job_assessment_schedule WHERE job_id=?", o.JobID).Scan(&pending))
	if !pending.Valid || pending.String == task {
		t.Fatal("later change lost", pending)
	}
	must(t, s.Assess(ctx, pending.String, o.JobID))
	job, err := s.Job(ctx, newObservation.JobID)
	must(t, err)
	if job.CurrentStatus != "UNKNOWN" {
		t.Fatal("latest fetch failure retained old OPEN status", job.CurrentStatus)
	}
	must(t, s.DB.QueryRowContext(ctx, "SELECT next_assess_at FROM job_assessment_schedule WHERE job_id=?", o.JobID).Scan(&due))
	if due.Valid {
		t.Fatal("failed observation causes recurring assessment", due)
	}
}

func TestAssessmentScheduleConcurrentDueAndExpiredLease(t *testing.T) {
	ctx, s, _, _, source := setup(t)
	o, err := s.Ingest(ctx, ingest(t, ctx, s, source))
	must(t, err)
	var old string
	must(t, s.DB.QueryRowContext(ctx, "SELECT pending_task_id FROM job_assessment_schedule WHERE job_id=?", o.JobID).Scan(&old))
	// A live task prevents another scheduler from issuing redundant work.
	_, err = s.ScheduleAssessments(ctx, time.Now().UTC(), 100)
	must(t, err)
	var current string
	must(t, s.DB.QueryRowContext(ctx, "SELECT pending_task_id FROM job_assessment_schedule WHERE job_id=?", o.JobID).Scan(&current))
	if current != old {
		t.Fatal("live pending task replaced")
	}
	// Simulate a lost/terminal task and a time boundary. Two schedulers race.
	_, err = s.DB.ExecContext(ctx, "UPDATE job_assessment_schedule SET next_assess_at=?,pending_until=? WHERE job_id=?", time.Now().Add(-24*time.Hour), time.Now().Add(-time.Second), o.JobID)
	must(t, err)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.ScheduleAssessments(ctx, time.Now().UTC(), 100); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		must(t, err)
	}
	must(t, s.DB.QueryRowContext(ctx, "SELECT pending_task_id FROM job_assessment_schedule WHERE job_id=?", o.JobID).Scan(&current))
	if current == old {
		t.Fatal("expired lease was never repaired")
	}
	var queued int
	must(t, s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.task_type'))='ASSESS' AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.entity_id'))=?`, o.JobID).Scan(&queued))
	if queued != 2 {
		t.Fatal("concurrent scheduling duplicated tasks", queued)
	}
	// A delayed old task can assess current inputs, but cannot erase its successor.
	must(t, s.Assess(ctx, old, o.JobID))
	var pending sql.NullString
	must(t, s.DB.QueryRowContext(ctx, "SELECT pending_task_id FROM job_assessment_schedule WHERE job_id=?", o.JobID).Scan(&pending))
	if !pending.Valid || pending.String != current {
		t.Fatal("late task cleared newer pending identity", pending)
	}
	must(t, s.Assess(ctx, current, o.JobID))
	must(t, s.DB.QueryRowContext(ctx, "SELECT pending_task_id FROM job_assessment_schedule WHERE job_id=?", o.JobID).Scan(&pending))
	if pending.Valid {
		t.Fatal("new task did not complete", pending)
	}
}

func TestAssessmentScheduleBootstrapAndRestartPreserveFuturePlan(t *testing.T) {
	ctx, s, _, _, source := setup(t)
	o, err := s.Ingest(ctx, ingest(t, ctx, s, source))
	must(t, err)
	_, err = s.DB.ExecContext(ctx, "DELETE FROM job_assessment_schedule WHERE job_id=?", o.JobID)
	must(t, err)
	must(t, s.InitAssessmentSchedule(ctx))
	var due sql.NullTime
	must(t, s.DB.QueryRowContext(ctx, "SELECT next_assess_at FROM job_assessment_schedule WHERE job_id=?", o.JobID).Scan(&due))
	if !due.Valid || due.Time.After(time.Now()) {
		t.Fatal("legacy job not backfilled", due)
	}
	must(t, s.Assess(ctx, d.ID(), o.JobID))
	must(t, s.DB.QueryRowContext(ctx, "SELECT next_assess_at FROM job_assessment_schedule WHERE job_id=?", o.JobID).Scan(&due))
	before := due
	must(t, s.InitAssessmentSchedule(ctx))
	must(t, s.DB.QueryRowContext(ctx, "SELECT next_assess_at FROM job_assessment_schedule WHERE job_id=?", o.JobID).Scan(&due))
	if due != before {
		t.Fatal("restart reset an unchanged plan", before, due)
	}
	_, err = s.DB.ExecContext(ctx, "UPDATE job_assessment_schedule SET rule_version='older-rules' WHERE job_id=?", o.JobID)
	must(t, err)
	must(t, s.InitAssessmentSchedule(ctx))
	must(t, s.DB.QueryRowContext(ctx, "SELECT next_assess_at FROM job_assessment_schedule WHERE job_id=?", o.JobID).Scan(&due))
	if !due.Valid || due.Time.After(time.Now()) {
		t.Fatal("rule change was ignored")
	}
	if _, err = s.ScheduleAssessments(ctx, time.Time{}, 1); err != p.ErrValidation {
		t.Fatal("invalid scheduling time accepted", err)
	}
}

func TestAssessmentScheduleClosesAtDeadlineWithoutNewObservation(t *testing.T) {
	ctx, s, q, _, source := setup(t)
	in := ingest(t, ctx, s, source)
	deadline := time.Now().UTC().Add(2 * time.Second).Truncate(time.Microsecond)
	in.Text += "\ndeadline: " + deadline.Format(time.RFC3339Nano)
	o, err := s.Ingest(ctx, in)
	must(t, err)
	must(t, worker(s, q).Process(ctx, p.NewTask("ANALYZE", o.ID)))
	var task string
	must(t, s.DB.QueryRowContext(ctx, "SELECT pending_task_id FROM job_assessment_schedule WHERE job_id=?", o.JobID).Scan(&task))
	must(t, s.Assess(ctx, task, o.JobID))
	job, err := s.Job(ctx, o.JobID)
	must(t, err)
	if job.CurrentStatus != "OPEN" {
		t.Fatal("fixture was not open before deadline", job.CurrentStatus)
	}
	var due sql.NullTime
	must(t, s.DB.QueryRowContext(ctx, "SELECT next_assess_at FROM job_assessment_schedule WHERE job_id=?", o.JobID).Scan(&due))
	if !due.Valid || !due.Time.Equal(deadline) {
		t.Fatal("deadline was not recorded", due, deadline)
	}
	<-time.After(time.Until(deadline) + 20*time.Millisecond)
	_, err = s.ScheduleAssessments(ctx, time.Now().UTC(), 100)
	must(t, err)
	var next string
	must(t, s.DB.QueryRowContext(ctx, "SELECT pending_task_id FROM job_assessment_schedule WHERE job_id=?", o.JobID).Scan(&next))
	if next == task {
		t.Fatal("deadline did not generate new work")
	}
	must(t, s.Assess(ctx, next, o.JobID))
	job, err = s.Job(ctx, o.JobID)
	must(t, err)
	if job.CurrentStatus != "CLOSED" {
		t.Fatal("deadline transition required a new fetch", job.CurrentStatus)
	}
	var observations, history int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE job_id=?", o.JobID).Scan(&observations))
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM assessments WHERE job_id=?", o.JobID).Scan(&history))
	if observations != 1 || history != 2 {
		t.Fatal("unexpected fetch or repeated history", observations, history)
	}
}

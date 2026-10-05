package integration

import (
	"context"
	"encoding/json"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/pipeline"
	"sync"
	"testing"
	"time"
)

func radarWatch(t *testing.T, ctx context.Context, s *p.Store, user, src string) d.WatchTarget {
	t.Helper()
	// Adapter configuration is operator-owned, never set by the authenticated user.
	_, err := s.DB.ExecContext(ctx, "UPDATE sources SET body=JSON_SET(body,'$.adapter','lever','$.tenant','fixture') WHERE id=?", src)
	must(t, err)
	w, err := s.CreateWatch(ctx, user, d.WatchInput{SourceID: src, CheckInterval: 3600, Enabled: true})
	must(t, err)
	// Some tests explicitly delete the watch before fixture cleanup. Its late
	// tasks remain valid stale-task tests, but must not leak into another worker.
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := s.DB.ExecContext(cleanup, `DELETE FROM outbox WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.watch_id'))=?`, w.ID); err != nil {
			t.Error(err)
		}
	})
	return w
}
func TestRadarScheduleAndIngestionReceipts(t *testing.T) {
	ctx, s, _, u, src := radarSetup(t)
	w := radarWatch(t, ctx, s, u, src)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.ScheduleWatches(ctx, time.Now().UTC(), 100); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		must(t, e)
	}
	tasks, err := p.Many[p.Task](ctx, s.DB, "SELECT body FROM outbox WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.watch_id'))=?", w.ID)
	must(t, err)
	if len(tasks) != 1 || tasks[0].ScheduleVersion != 1 {
		t.Fatalf("not a single generation: %+v", tasks)
	}
	task := tasks[0]
	in := ingest(t, ctx, s, src)
	in.ObservedAt = time.Now().UTC()
	o, err := s.IngestWatch(ctx, task, in)
	must(t, err)
	duplicate, err := s.IngestWatch(ctx, task, in)
	must(t, err)
	if duplicate.ID != o.ID {
		t.Fatal("duplicate observation")
	}
	other, err := s.NewUser(ctx, d.ID()+"@radar.invalid", "unused")
	must(t, err)
	if _, err = s.Watch(ctx, other, w.ID); !errors.Is(err, p.ErrNotFound) {
		t.Fatal("watch ownership", err)
	}
	if err = s.DeleteWatch(ctx, other, w.ID); !errors.Is(err, p.ErrNotFound) {
		t.Fatal("watch delete ownership", err)
	}
	_, err = s.UpdateWatch(ctx, u, w.ID, d.WatchInput{SourceID: src, CheckInterval: 7200, Enabled: true})
	must(t, err)
	in.ExternalID = d.ID()
	if _, err = s.IngestWatch(ctx, task, in); !errors.Is(err, p.ErrStaleWatch) {
		t.Fatal("old config generation accepted", err)
	}
	_, err = s.UpdateWatch(ctx, u, w.ID, d.WatchInput{SourceID: src, CheckInterval: 7200, Enabled: false})
	must(t, err)
	if _, err = s.IngestWatch(ctx, task, in); !errors.Is(err, p.ErrStaleWatch) {
		t.Fatal("disabled late task accepted", err)
	}
	must(t, s.DeleteWatch(ctx, u, w.ID))
}
func TestRadarPreferencesAndDigest(t *testing.T) {
	ctx, s, q, u, src := radarSetup(t)
	in := ingest(t, ctx, s, src)
	in.Text = jd + "\ndeadline: " + time.Now().Add(60*time.Hour).UTC().Format(time.RFC3339)
	o, err := s.Ingest(ctx, in)
	must(t, err)
	must(t, s.SaveProfile(ctx, u, d.Profile{GraduationYear: 2027, Degree: "BACHELOR", Skills: []string{"go"}}))
	must(t, worker(s, q).Process(ctx, p.NewTask("ANALYZE", o.ID)))
	must(t, s.Assess(ctx, d.ID(), o.JobID))
	digest, err := s.DailyDigest(ctx, u)
	must(t, err)
	if len(digest.ClosingSoon) == 0 {
		t.Fatal("deadline missing")
	}
	for i := 0; i < 2; i++ {
		_, _, err = s.RefreshNotifications(ctx, u)
		must(t, err)
	}
	ns, err := s.Notifications(ctx, u)
	must(t, err)
	count := 0
	for _, n := range ns {
		if n.Type == "JOB_CLOSING_SOON" && n.EntityID == o.JobID {
			count++
		}
	}
	if count != 1 {
		t.Fatal("deadline dedup", count)
	}
	must(t, s.SetPreference(ctx, u, o.JobID, "IGNORED"))
	digest, err = s.DailyDigest(ctx, u)
	must(t, err)
	for _, j := range digest.RecommendedJobs {
		if j.Job.ID == o.JobID {
			t.Fatal("ignored recommendation")
		}
	}
	for _, j := range digest.ClosingSoon {
		if j.Job.ID == o.JobID {
			t.Fatal("ignored deadline")
		}
	}
	must(t, s.SetPreference(ctx, u, o.JobID, "NONE"))
	raw, err := s.ApplyAction(ctx, u, d.ID(), "create_application", []byte(d.JSON(p.CreateArgs{JobID: o.JobID})))
	must(t, err)
	var app d.Application
	must(t, json.Unmarshal(raw, &app))
	_, err = s.ApplyAction(ctx, u, d.ID(), "transition_application", []byte(d.JSON(p.TransitionArgs{ApplicationID: app.ID, Version: 1, State: "WITHDRAWN"})))
	must(t, err)
	digest, err = s.DailyDigest(ctx, u)
	must(t, err)
	for _, j := range digest.RecommendedJobs {
		if j.Job.ID == o.JobID {
			t.Fatal("withdrawn recommendation")
		}
	}
}

// Radar fixtures must not leak enabled watches or asynchronous work into the
// existing running-worker tests (which use their own registered RPC service).
func radarSetup(t *testing.T) (context.Context, *p.Store, *pipeline.Queue, string, string) {
	ctx, s, q, u, src := setup(t)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, query := range []string{
			`DELETE FROM outbox WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.watch_id')) IN (SELECT id FROM watch_targets WHERE source_id=?)`,
			`DELETE FROM outbox WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.entity_id')) IN (SELECT o.id FROM observations o JOIN postings p ON p.id=o.posting_id WHERE p.source_id=?)`,
			`DELETE FROM outbox WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.entity_id')) IN (SELECT job_id FROM postings WHERE source_id=?)`,
			`DELETE FROM watch_targets WHERE source_id=?`,
		} {
			if _, err := s.DB.ExecContext(cleanup, query, src); err != nil {
				t.Error(err)
			}
		}
		if _, err := s.DB.ExecContext(cleanup, "DELETE FROM profiles WHERE user_id=?", u); err != nil {
			t.Error(err)
		}
	})
	return ctx, s, q, u, src
}

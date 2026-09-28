package integration

import (
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"testing"
	"time"
)

func TestAdaptiveWatchRoundChangeFailureAndGeneration(t *testing.T) {
	ctx, s, _, u, src := radarSetup(t)
	w := radarWatch(t, ctx, s, u, src)
	input := w.WatchInput
	input.Adaptive = true
	w, e := s.UpdateWatch(ctx, u, w.ID, input)
	must(t, e)
	now := time.Now().UTC()
	in := ingest(t, ctx, s, src)
	in.ExternalID = "stable"
	in.ObservedAt = now
	// A pre-existing successful posting provides a stable comparison baseline.
	_, e = s.Ingest(ctx, in)
	must(t, e)
	var last p.Task
	for round := 0; round < 4; round++ {
		_, e = s.ScheduleWatches(ctx, now, 100)
		must(t, e)
		w, e = s.Watch(ctx, u, w.ID)
		must(t, e)
		last = p.WatchTask(w)
		must(t, s.QueueWatchPostings(ctx, last, []p.Ingest{in}, d.Hash("same complete discovery")))
		tasks, e := p.Many[p.Task](ctx, s.DB, "SELECT body FROM outbox WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.watch_id'))=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.task_type'))='WATCH_FETCH' ORDER BY created_at DESC", w.ID)
		must(t, e)
		var child p.Task
		for _, task := range tasks {
			if task.ScheduleVersion == w.ScheduleVersion {
				child = task
				break
			}
		}
		in.ObservedAt = time.Now().UTC()
		_, e = s.IngestWatch(ctx, child, in)
		must(t, e)
		succeeded, e := s.CompleteWatchFetch(ctx, child, false)
		must(t, e)
		if !succeeded {
			t.Fatal("successful round")
		}
		duplicate, e := s.CompleteWatchFetch(ctx, child, false)
		must(t, e)
		if duplicate {
			t.Fatal("duplicate round counted")
		}
		w, e = s.Watch(ctx, u, w.ID)
		must(t, e)
		now = w.NextCheckAt.Add(time.Second)
	}
	if w.StableRounds != 3 || w.EffectiveInterval != 7200 {
		t.Fatalf("unchanged rounds not slowed: %+v", w)
	}
	// Old generations cannot alter the adaptive counters.
	before := d.JSON(w)
	must(t, s.FinishWatch(ctx, p.WatchTask(d.WatchTarget{ID: w.ID, ScheduleVersion: 1}), "FAILED"))
	w, e = s.Watch(ctx, u, w.ID)
	must(t, e)
	if d.JSON(w) != before {
		t.Fatal("stale generation changed state")
	}
	_, e = s.ScheduleWatches(ctx, now, 100)
	must(t, e)
	w, e = s.Watch(ctx, u, w.ID)
	must(t, e)
	last = p.WatchTask(w)
	must(t, s.FinishWatch(ctx, last, "FAILED"))
	must(t, s.FinishWatch(ctx, last, "FAILED"))
	w, e = s.Watch(ctx, u, w.ID)
	must(t, e)
	if w.FailureRounds != 1 || w.EffectiveInterval != 7200 {
		t.Fatalf("failure not counted once: %+v", w)
	}
}

func TestAdaptiveDeadlineUsesNewestActualObservation(t *testing.T) {
	ctx, s, _, u, src := radarSetup(t)
	w := radarWatch(t, ctx, s, u, src)
	input := w.WatchInput
	input.Adaptive = true
	w, e := s.UpdateWatch(ctx, u, w.ID, input)
	must(t, e)
	w.StableRounds = 6
	_, e = s.DB.ExecContext(ctx, "UPDATE watch_targets SET body=? WHERE id=?", d.JSON(w), w.ID)
	must(t, e)
	in := ingest(t, ctx, s, src)
	o, e := s.Ingest(ctx, in)
	must(t, e)
	_, e = s.DB.ExecContext(ctx, "UPDATE observations SET body=JSON_SET(body,'$.deadline_signal',?) WHERE id=?", time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339), o.ID)
	must(t, e)
	_, e = s.DB.ExecContext(ctx, "INSERT INTO watch_postings(watch_id,posting_key,body) VALUES(?,?,?)", w.ID, p.SourceKey(in), d.JSON(in))
	must(t, e)
	_, e = s.ScheduleWatches(ctx, time.Now().UTC(), 100)
	must(t, e)
	w, e = s.Watch(ctx, u, w.ID)
	must(t, e)
	if w.EffectiveInterval != 1800 || w.ScheduleReason != "PRIORITY" {
		t.Fatal("deadline not prioritized", w)
	}
	must(t, s.QueueWatchPostings(ctx, p.WatchTask(w), nil))
	w, e = s.Watch(ctx, u, w.ID)
	must(t, e)
	in.FetchStatus = "TIMEOUT"
	in.Text = ""
	in.ObservedAt = time.Now().Add(time.Millisecond).UTC()
	_, e = s.Ingest(ctx, in)
	must(t, e)
	_, e = s.ScheduleWatches(ctx, w.NextCheckAt.Add(time.Second), 100)
	must(t, e)
	w, e = s.Watch(ctx, u, w.ID)
	must(t, e)
	if w.ScheduleReason == "PRIORITY" {
		t.Fatal("latest failure fell back to old successful deadline", w)
	}
	must(t, s.QueueWatchPostings(ctx, p.WatchTask(w), nil, d.Hash("complete list disappeared")))
	w, e = s.Watch(ctx, u, w.ID)
	must(t, e)
	if w.ScheduleReason != "RECENT_CHANGE" {
		t.Fatal("disappearance ignored for scheduling", w)
	}
	j, e := s.Job(ctx, o.JobID)
	must(t, e)
	if j.CurrentStatus == "CLOSED" {
		t.Fatal("disappearance proved closure")
	}
}

func TestAdaptiveWatchDoesNotReplaceActiveRoundAndCacheBounded(t *testing.T) {
	ctx, s, _, u, src := radarSetup(t)
	w := radarWatch(t, ctx, s, u, src)
	input := w.WatchInput
	input.Adaptive = true
	input.Priority = true
	w, e := s.UpdateWatch(ctx, u, w.ID, input)
	must(t, e)
	started := time.Now().UTC()
	_, e = s.ScheduleWatches(ctx, started, 100)
	must(t, e)
	w, e = s.Watch(ctx, u, w.ID)
	must(t, e)
	version := w.ScheduleVersion
	_, e = s.ScheduleWatches(ctx, w.NextCheckAt.Add(time.Second), 100)
	must(t, e)
	w, e = s.Watch(ctx, u, w.ID)
	must(t, e)
	if version != w.ScheduleVersion {
		t.Fatal("replaced in-flight round")
	}
	_, e = s.ScheduleWatches(ctx, started.Add(2*time.Hour), 100)
	must(t, e)
	w, e = s.Watch(ctx, u, w.ID)
	must(t, e)
	if w.ScheduleVersion != version+1 || w.FailureRounds != 1 {
		t.Fatal("stuck generation not fenced", w)
	}
	for i := 0; i < 66; i++ {
		must(t, s.SourceHTTPWrite(ctx, src, d.Hash(string(rune(i))), p.SourceHTTPEntry{ETag: "version", Body: []byte(`{"x":1}`)}))
	}
	var count int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM source_http_cache WHERE source_id=?", src).Scan(&count))
	if count != 64 {
		t.Fatal("cache unbounded", count)
	}
	if _, e = s.SourceHTTPRead(ctx, src, d.Hash(string(rune(65)))); e != nil {
		t.Fatal(e)
	}
	_, e = s.DB.ExecContext(ctx, "UPDATE source_http_cache SET updated_at=UTC_TIMESTAMP(6)-INTERVAL 2 DAY WHERE source_id=?", src)
	must(t, e)
	if _, e = s.SourceHTTPRead(ctx, src, d.Hash(string(rune(65)))); e == nil {
		t.Fatal("used expired validator")
	}
}

func TestRetentionProtectsEvidenceReceiptsAndUnfinishedTasks(t *testing.T) {
	ctx, s, _, u, src := radarSetup(t)
	in := ingest(t, ctx, s, src)
	o, e := s.Ingest(ctx, in)
	must(t, e)
	old := time.Now().UTC().Add(-100 * 24 * time.Hour)
	before := time.Now().UTC().Add(-90 * 24 * time.Hour)
	ids := []string{d.ID(), d.ID(), d.ID()}
	for i, id := range ids {
		task := p.NewTask("ASSESS", o.JobID)
		task.ID = id
		_, e = s.DB.ExecContext(ctx, "INSERT INTO outbox(id,body,sent,created_at) VALUES(?,?,?,?)", id, d.JSON(task), i != 1, old)
		must(t, e)
		if i != 2 {
			_, e = s.DB.ExecContext(ctx, "INSERT INTO completed_tasks(id,completed_at) VALUES(?,?)", id, old)
			must(t, e)
		}
	}
	runIDs := []string{d.ID(), d.ID()}
	events := []string{d.ID(), d.ID()}
	for i, id := range runIDs {
		state := "COMPLETED"
		if i == 1 {
			state = "WAITING_AUTH"
		}
		_, e = s.DB.ExecContext(ctx, "INSERT INTO match_runs(id,user_id,request_key,state,updated_at,body) VALUES(?,?,?,?,?,?)", id, u, d.ID(), state, old, `{}`)
		must(t, e)
		_, e = s.DB.ExecContext(ctx, "INSERT INTO match_run_events(id,run_id,occurred_at,body) VALUES(?,?,?,?)", events[i], id, old, `{}`)
		must(t, e)
	}
	preview, e := s.RetainHistory(ctx, before, false)
	must(t, e)
	if preview.Rows["outbox"] < 1 || preview.Rows["match_run_events"] < 1 {
		t.Fatal(preview)
	}
	var exists int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM outbox WHERE id=?", ids[0]).Scan(&exists))
	if exists != 1 {
		t.Fatal("preview deleted data")
	}
	_, e = s.RetainHistory(ctx, before, true)
	must(t, e)
	checks := []struct {
		q, id string
		want  int
	}{{"SELECT COUNT(*) FROM observations WHERE id=?", o.ID, 1}, {"SELECT COUNT(*) FROM completed_tasks WHERE id=?", ids[0], 1}, {"SELECT COUNT(*) FROM outbox WHERE id=?", ids[0], 0}, {"SELECT COUNT(*) FROM outbox WHERE id=?", ids[1], 1}, {"SELECT COUNT(*) FROM outbox WHERE id=?", ids[2], 1}, {"SELECT COUNT(*) FROM match_run_events WHERE id=?", events[0], 0}, {"SELECT COUNT(*) FROM match_run_events WHERE id=?", events[1], 1}}
	for _, c := range checks {
		must(t, s.DB.QueryRowContext(ctx, c.q, c.id).Scan(&exists))
		if exists != c.want {
			t.Fatal("retention removed protected record", c)
		}
	}
}

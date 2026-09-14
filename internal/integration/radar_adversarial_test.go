package integration

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	"github.com/KanaDoodle/CampusTrace/internal/config"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/pipeline"
	"github.com/KanaDoodle/CampusTrace/internal/source"
	"github.com/redis/go-redis/v9"
	"strings"
	"sync"
	"testing"
	"time"
)

type radarAdapter struct {
	ref    source.PostingRef
	result source.Result
	err    error
}

func (a *radarAdapter) Discover(context.Context, d.Source, d.WatchTarget) ([]source.PostingRef, error) {
	return []source.PostingRef{a.ref}, nil
}
func (a *radarAdapter) FetchPosting(context.Context, d.Source, source.PostingRef) (source.Result, error) {
	return a.result, a.err
}
func scheduledWatch(t *testing.T, ctx context.Context, s *p.Store, u, src string) (d.WatchTarget, p.Task) {
	t.Helper()
	v := radarWatch(t, ctx, s, u, src)
	_, err := s.ScheduleWatches(ctx, time.Now().UTC(), 100)
	must(t, err)
	task, err := p.One[p.Task](ctx, s.DB, "SELECT body FROM outbox WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.watch_id'))=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.task_type'))='WATCH_CHECK' ORDER BY created_at DESC LIMIT 1", v.ID)
	must(t, err)
	return v, task
}
func TestRadarOutboxDuplicateAndFanoutRecovery(t *testing.T) {
	ctx, s, q, u, src := radarSetup(t)
	v, task := scheduledWatch(t, ctx, s, u, src)
	must(t, q.Init(ctx))
	w := worker(s, q)
	a := &radarAdapter{ref: source.PostingRef{ExternalID: d.ID(), URL: "https://jobs.lever.co/fixture/CaseA", Title: "校招 Go", Company: "Fixture " + d.ID(), JobType: "FULL_TIME", Locations: []string{"Shanghai"}}, result: source.Result{Text: jd, Status: "SUCCESS", HTTPStatus: 200}}
	w.Source = a
	// The transaction is already committed; an independently restarted publisher
	// recovers the unsent row. Publishing once without marking sent simulates crash.
	must(t, q.Publish(ctx, task))
	must(t, w.Dispatch(ctx))
	if n, err := q.R.XLen(ctx, q.Stream()).Result(); err != nil || n < 2 {
		t.Fatal("outbox recovery", n, err)
	}
	must(t, w.Process(ctx, task))
	must(t, w.Process(ctx, task))
	children, err := p.Many[p.Task](ctx, s.DB, "SELECT body FROM outbox WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.watch_id'))=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.task_type'))='WATCH_FETCH'", v.ID)
	must(t, err)
	if len(children) != 1 {
		t.Fatal("duplicate fanout", len(children))
	}
	child := children[0]
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- w.Process(ctx, child) }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		must(t, e)
	}
	var n int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM watch_results WHERE watch_id=? AND outcome='SUCCESS'", v.ID).Scan(&n))
	if n != 1 {
		t.Fatal("duplicate result", n)
	}
	current, err := s.Watch(ctx, u, v.ID)
	must(t, err)
	if current.LastOutcome != "SUCCESS" || current.LastCheckedAt == nil {
		t.Fatalf("not completed %+v", current)
	}
	o, err := s.WatchObservation(ctx, child, *child.Posting)
	must(t, err)
	must(t, w.Process(ctx, p.NewTask("ANALYZE", o.ID)))
	must(t, s.Assess(ctx, d.ID(), o.JobID))
	j, err := s.Job(ctx, o.JobID)
	must(t, err)
	if j.CurrentStatus != "OPEN" {
		t.Fatal("did not use existing analysis chain", j.CurrentStatus)
	}
}
func TestRadarFetchFailureAndLateGeneration(t *testing.T) {
	ctx, s, q, u, src := radarSetup(t)
	v, task := scheduledWatch(t, ctx, s, u, src)
	w := worker(s, q)
	a := &radarAdapter{ref: source.PostingRef{ExternalID: d.ID(), URL: "https://jobs.lever.co/fixture/CaseA", Title: "Go", Company: "Failure " + d.ID(), JobType: "FULL_TIME"}, result: source.Result{Status: "HTTP_ERROR", HTTPStatus: 503}, err: &source.FetchError{Category: "HTTP_TRANSIENT", Retryable: true, HTTPStatus: 503}}
	w.Source = a
	must(t, w.Process(ctx, task))
	child, err := p.One[p.Task](ctx, s.DB, "SELECT body FROM outbox WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.watch_id'))=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.task_type'))='WATCH_FETCH'", v.ID)
	must(t, err)
	for i := 0; i < 2; i++ {
		err = w.Process(ctx, child)
		if err == nil || !pipeline.Transient(err) {
			t.Fatal("not retryable", err)
		}
	}
	obs, err := p.Many[d.Observation](ctx, s.DB, "SELECT o.body FROM observations o JOIN watch_results r ON r.observation_id=o.id WHERE r.watch_id=?", v.ID)
	must(t, err)
	if len(obs) != 1 {
		t.Fatal("duplicate failure observations", len(obs))
	}
	must(t, w.Process(ctx, p.NewTask("ANALYZE", obs[0].ID)))
	must(t, s.Assess(ctx, d.ID(), obs[0].JobID))
	j, err := s.Job(ctx, obs[0].JobID)
	must(t, err)
	if j.CurrentStatus == "CLOSED" {
		t.Fatal("fetch failure closed job")
	}
	a.result = source.Result{Text: jd, Status: "SUCCESS", HTTPStatus: 200}
	a.err = nil
	child.Attempt++
	must(t, w.Process(ctx, child))
	// An interval/config edit invalidates both listing and posting envelopes.
	_, err = s.UpdateWatch(ctx, u, v.ID, d.WatchInput{SourceID: src, Enabled: true, CheckInterval: 300, Keyword: "changed"})
	must(t, err)
	must(t, w.Process(ctx, child))
	newWatch, err := s.Watch(ctx, u, v.ID)
	must(t, err)
	if newWatch.LastOutcome != "PENDING" {
		t.Fatal("late result overwrote configuration")
	}
	_, err = s.UpdateWatch(ctx, u, v.ID, d.WatchInput{SourceID: src, Enabled: false, CheckInterval: 300})
	must(t, err)
	must(t, w.Process(ctx, task))
	_, err = s.ScheduleWatches(ctx, time.Now().Add(time.Hour), 100)
	must(t, err)
	newWatch, err = s.Watch(ctx, u, v.ID)
	must(t, err)
	if newWatch.Enabled || newWatch.LastOutcome != "PENDING" {
		t.Fatal("disabled watch advanced")
	}
	if pipeline.Transient(&source.FetchError{Category: "UNSUPPORTED"}) {
		t.Fatal("unsupported retries forever")
	}
}
func TestRadarNotificationVersionsAndIsolation(t *testing.T) {
	ctx, s, q, u, src := radarSetup(t)
	in := ingest(t, ctx, s, src)
	in.ObservedAt = time.Now().UTC().Add(-time.Minute)
	deadline := time.Now().Add(60 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)
	in.Text = jd + "\ndeadline: " + deadline
	must(t, s.SaveProfile(ctx, u, d.Profile{GraduationYear: 2027, Degree: "BACHELOR", Skills: []string{"go"}}))
	w := worker(s, q)
	observe := func() d.Observation {
		in.ObservedAt = in.ObservedAt.Add(time.Second)
		o, err := s.Ingest(ctx, in)
		must(t, err)
		must(t, w.Process(ctx, p.NewTask("ANALYZE", o.ID)))
		must(t, s.Assess(ctx, d.ID(), o.JobID))
		return o
	}
	o := observe()
	refresh := func() { _, _, err := s.RefreshNotifications(ctx, u); must(t, err) }
	refresh()
	observe()
	refresh()
	observe()
	refresh()
	count := func(typ string) int {
		ns, err := s.Notifications(ctx, u)
		must(t, err)
		n := 0
		for _, v := range ns {
			if v.EntityID == o.JobID && v.Type == typ {
				n++
			}
		}
		return n
	}
	if n := count("JOB_CLOSING_SOON"); n != 1 {
		t.Fatal("same deadline repeated", n)
	}
	in.Text = jd + "\ndeadline: " + time.Now().Add(65*time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)
	observe()
	refresh()
	if n := count("JOB_CLOSING_SOON"); n != 2 {
		t.Fatal("modified deadline not notified", n)
	}
	in.Text = jd + "\ndeadline: " + deadline
	observe()
	refresh()
	if n := count("JOB_CLOSING_SOON"); n != 3 {
		t.Fatal("deadline reverting needs new semantic version", n)
	}
	in.Text = "closed: CLOSED\ngraduation: 2027\ndegree: BACHELOR"
	observe()
	refresh()
	observe()
	refresh()
	if n := count("JOB_STATUS_CHANGED"); n != 1 {
		t.Fatal("status transition repeated", n)
	}
	changes, err := s.RecentChanges(ctx, u, 1)
	must(t, err)
	types := map[string]bool{}
	for _, c := range changes {
		if c.JobID == o.JobID {
			types[c.Type] = true
		}
	}
	for _, kind := range []string{"NEW_JOB", "JOB_CONTENT_CHANGED", "JOB_STATUS_CHANGED", "DEADLINE_CHANGED"} {
		if !types[kind] {
			t.Fatal("missing change", kind)
		}
	}
	b, err := s.NewUser(ctx, d.ID()+"@radar-other.invalid", "unused")
	must(t, err)
	ns, err := s.Notifications(ctx, u)
	must(t, err)
	for _, n := range ns {
		if err = s.ReadNotification(ctx, b, n.ID); !errors.Is(err, p.ErrNotFound) {
			t.Fatal("foreign inbox write", err)
		}
	}
	bInbox, err := s.Notifications(ctx, b)
	must(t, err)
	if len(bInbox) != 0 {
		t.Fatal("foreign inbox leak")
	}
	private, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Private", Title: "Private JD", JobType: "UNKNOWN", Text: "private", FetchStatus: "SUCCESS"})
	must(t, err)
	must(t, s.SetPreference(ctx, u, private.JobID, "SAVED"))
	if err = s.SetPreference(ctx, b, private.JobID, "IGNORED"); !errors.Is(err, p.ErrNotFound) {
		t.Fatal("private preference write", err)
	}
	prefs, err := s.Preferences(ctx, b)
	must(t, err)
	for _, v := range prefs {
		if v.UserID == u || v.JobID == private.JobID {
			t.Fatal("preference leak")
		}
	}
}
func TestRadarAgentGroundingAndConfirmation(t *testing.T) {
	ctx, s, q, u, src := radarSetup(t)
	watch := radarWatch(t, ctx, s, u, src)
	tools := &agent.Tools{Store: s, Queue: q}
	args := []byte(d.JSON(d.WatchInput{SourceID: src, CheckInterval: 3600, Enabled: true, Keyword: "Go"}))
	proposal, err := tools.Execute(ctx, u, "watch_source", args)
	must(t, err)
	pending := proposal.(agent.Pending)
	watches, err := s.Watches(ctx, u)
	must(t, err)
	if len(watches) != 1 {
		t.Fatal("proposal wrote business truth")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := tools.Confirm(ctx, u, pending.ID); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		must(t, e)
	}
	watches, err = s.Watches(ctx, u)
	must(t, err)
	if len(watches) != 2 {
		t.Fatal("confirmation duplicate")
	}
	b, err := s.NewUser(ctx, d.ID()+"@agent-radar.invalid", "unused")
	must(t, err)
	if _, err = tools.Confirm(ctx, b, pending.ID); !errors.Is(err, p.ErrNotFound) {
		t.Fatal("foreign receipt", err)
	}
	var before, after int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM notifications WHERE user_id=?", u).Scan(&before))
	model := &agent.Scripted{Replies: []agent.Reply{{Calls: []agent.Call{{ID: "radar", Name: "get_daily_digest", Args: json.RawMessage(`{}`)}}}, {Text: "Invented 999999 open jobs"}}}
	runtime := agent.Runtime{Model: model, Tools: tools, R: q.R, Prefix: q.Prefix, MaxModels: 3, MaxTools: 3, Deadline: 10 * time.Second, ToolTimeout: 8 * time.Second, MaxToolResultBytes: 1 << 20, MaxFactsBytes: 2 << 20, MaxFinalBytes: 3 << 20}
	result := runtime.Run(ctx, u, "radar-grounding", "今天有什么值得处理", nil)
	if result.Executed != 1 || len(result.Facts) != 1 || strings.Contains(result.Answer, "999999") {
		t.Fatalf("not grounded %+v", result)
	}
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM notifications WHERE user_id=?", u).Scan(&after))
	if before != after {
		t.Fatal("read tool wrote inbox")
	}
	_, err = tools.Execute(ctx, b, "unwatch_source", []byte(d.JSON(map[string]string{"watch_id": watch.ID})))
	if !errors.Is(err, p.ErrNotFound) {
		t.Fatal("foreign watch proposal", err)
	}
}

func TestRadarRetryQueueAndRateLimit(t *testing.T) {
	ctx, s, q, u, src := radarSetup(t)
	_, task := scheduledWatch(t, ctx, s, u, src)
	must(t, q.Init(ctx))
	must(t, q.Publish(ctx, task))
	messages, err := q.R.XReadGroup(ctx, &redis.XReadGroupArgs{Group: q.Group(), Consumer: "radar-retry", Streams: []string{q.Stream(), ">"}, Count: 1}).Result()
	must(t, err)
	rateErr := &source.FetchError{Category: "RATE_LIMIT", Retryable: true, HTTPStatus: 429}
	retry, err := q.Fail(ctx, messages[0].Messages[0].ID, task, rateErr, 4)
	must(t, err)
	if !retry {
		t.Fatal("rate limit not retried")
	}
	entries, err := q.R.ZRangeWithScores(ctx, q.Prefix+"retry:WATCH_CHECK", 0, -1).Result()
	must(t, err)
	if len(entries) != 1 || entries[0].Score < float64(time.Now().Add(55*time.Second).UnixMilli()) {
		t.Fatal("retry consumed budget inside rate window")
	}
	must(t, q.R.ZAdd(ctx, q.Prefix+"retry:WATCH_CHECK", redis.Z{Score: float64(time.Now().Add(-time.Second).UnixMilli()), Member: entries[0].Member}).Err())
	n, err := q.Schedule(ctx)
	must(t, err)
	if n != 1 {
		t.Fatal("watch retries not scheduled", n)
	}
	var next p.Task
	must(t, json.Unmarshal([]byte(entries[0].Member.(string)), &next))
	if next.ID != task.ID || next.ScheduleVersion != task.ScheduleVersion || next.Attempt != 2 {
		t.Fatal("retry changed business identity")
	}
}

func TestRadarWatchThroughRPC(t *testing.T) {
	ctx, s, q, u, src := radarSetup(t)
	watch := radarWatch(t, ctx, s, u, src)
	must(t, s.SaveProfile(ctx, u, d.Profile{GraduationYear: 2027, Degree: "BACHELOR", Skills: []string{"go"}}))
	serviceName := "radar-rpc-" + d.ID()
	server, err := analysis.StartServer(ctx, config.Load().Endpoints(), "127.0.0.1:0", "", "radar-analysis", analysis.RuleExtractor{}, 2, 0, serviceName)
	must(t, err)
	defer server.Close()
	rpc, err := analysis.NewClient(config.Load().Endpoints(), 1, time.Second, serviceName)
	must(t, err)
	defer rpc.Close()
	w := worker(s, q)
	w.Analyzer = rpc
	w.Source = &radarAdapter{ref: source.PostingRef{ExternalID: d.ID(), URL: "https://jobs.lever.co/fixture/RPC", Company: "Radar RPC " + d.ID(), Title: "校招 Go", JobType: "FULL_TIME", Locations: []string{"Shanghai"}}, result: source.Result{Text: jd + "\ndeadline: " + time.Now().Add(48*time.Hour).UTC().Format(time.RFC3339), Status: "SUCCESS", HTTPStatus: 200}}
	runctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(runctx) }()
	defer func() { cancel(); must(t, <-done) }()
	until := time.Now().Add(10 * time.Second)
	for {
		v, err := s.Watch(ctx, u, watch.ID)
		must(t, err)
		jobs, err := p.Many[d.Job](ctx, s.DB, "SELECT j.body FROM jobs j JOIN postings p ON p.job_id=j.id WHERE p.source_id=?", src)
		must(t, err)
		if v.LastOutcome == "SUCCESS" && len(jobs) == 1 && jobs[0].CurrentStatus == "OPEN" {
			_, _, err = s.RefreshNotifications(ctx, u)
			must(t, err)
			ns, err := s.Notifications(ctx, u)
			must(t, err)
			for _, n := range ns {
				if n.EntityID == jobs[0].ID && n.Type == "JOB_CLOSING_SOON" {
					return
				}
			}
		}
		if time.Now().After(until) {
			t.Fatal("scheduled watch did not reach RPC assessment and notification")
		}
		time.Sleep(30 * time.Millisecond)
	}
}

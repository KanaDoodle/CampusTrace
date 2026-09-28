package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func awaitRun(t *testing.T, s *p.Store, user, id, state string) p.MatchRun {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		v, err := s.MatchRun(context.Background(), user, id)
		must(t, err)
		if v.State == state {
			return v
		}
		time.Sleep(20 * time.Millisecond)
	}
	v, _ := s.MatchRun(context.Background(), user, id)
	t.Fatalf("task did not reach %s: %s", state, d.JSON(v))
	return v
}
func taskJobs(t *testing.T, ctx context.Context, s *p.Store, user string, n int) []string {
	t.Helper()
	must(t, s.SaveProfile(ctx, user, d.Profile{GraduationYear: 2027, Degree: "MASTER", Languages: []string{"Go"}}))
	ids := []string{}
	for i := 0; i < n; i++ {
		o, err := s.IngestForUser(ctx, user, p.Ingest{Company: "Durable fixture " + user, Title: "后端开发", JobType: "FULL_TIME", ExternalID: d.ID(), Text: "掌握 Go，负责服务端开发", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
		must(t, err)
		ids = append(ids, o.JobID)
	}
	return ids
}
func TestDurableMatchingSurvivesRequestEndAndIdempotentReplay(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	ids := taskJobs(t, ctx, s, u, 2)
	inner := &matchFixtureModel{}
	blocked := &blockingMatchModel{inner: inner, entered: make(chan struct{}), release: make(chan struct{})}
	authn := auth.Service{Store: s, Secret: []byte("durable-http-integration-secret-32")}
	token, err := authn.Token(u)
	must(t, err)
	a := &transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New(), ResumeModel: blocked, ResumeModelName: "fixture"}
	runner := a.StartMatchTasks(ctx, 2)
	defer runner.Close()
	h := a.Handler()
	snap, err := s.MatchSnapshot(ctx, u, matching.ModelIdentity("server", "fixture"), "", ids)
	must(t, err)
	// Obtain the exact configured identity through the public preview.
	rec := matchingRequest(h, token, "/api/matching/preview", "POST", map[string]any{})
	must(t, json.Unmarshal(rec.Body.Bytes(), &snap))
	body := map[string]any{"job_ids": ids, "candidate_hash": snap.CandidateHash, "input_keys": taskInputKeys(snap, ids), "request_key": d.ID()}
	bad := matchingRequest(h, token, "/api/matching/tasks", "POST", map[string]any{"job_ids": ids, "candidate_hash": snap.CandidateHash, "input_keys": map[string]string{ids[0]: "changed", ids[1]: "changed"}, "request_key": d.ID()})
	if bad.Code != 409 || inner.calls.Load() != 0 {
		t.Fatal("unreviewed changed text reached model", bad.Code, bad.Body.String())
	}
	requestCtx, cancel := context.WithCancel(ctx)
	req := httptest.NewRequest("POST", "/api/matching/tasks", bytes.NewBufferString(d.JSON(body))).WithContext(requestCtx)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 202 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var run p.MatchRun
	must(t, json.Unmarshal(rec.Body.Bytes(), &run))
	cancel()
	select {
	case <-blocked.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("background runner did not start")
	}
	replay := matchingRequest(h, token, "/api/matching/tasks", "POST", body)
	if replay.Code != 202 || !strings.Contains(replay.Body.String(), run.ID) {
		t.Fatal("request replay created another task", replay.Code, replay.Body.String())
	}
	another := matchingRequest(h, token, "/api/matching/tasks", "POST", map[string]any{"job_ids": ids, "candidate_hash": snap.CandidateHash, "input_keys": taskInputKeys(snap, ids), "request_key": d.ID()})
	if another.Code != 409 || !strings.Contains(another.Body.String(), "MATCH_BUSY") {
		t.Fatal("concurrent paid round accepted", another.Code, another.Body.String())
	}
	other, err := s.NewUser(ctx, d.ID()+"@task-owner.invalid", "unused")
	must(t, err)
	otherToken, err := authn.Token(other)
	must(t, err)
	for _, suffix := range []string{"", "/events"} {
		r := matchingRequest(h, otherToken, "/api/matching/tasks/"+run.ID+suffix, "GET", nil)
		if r.Code != 404 {
			t.Fatal("task ownership leak", r.Code)
		}
	}
	close(blocked.release)
	done := awaitRun(t, s, u, run.ID, "COMPLETED")
	if done.Calls != 2 || inner.calls.Load() != 2 || len(done.Items) != 2 {
		t.Fatal(done, inner.calls.Load())
	}
	for _, i := range done.Items {
		if i.State != "SUCCEEDED" {
			t.Fatal(i)
		}
	}
	replay = matchingRequest(h, token, "/api/matching/tasks", "POST", body)
	if replay.Code != 202 || inner.calls.Load() != 2 {
		t.Fatal("completed replay billed again", replay.Code, replay.Body.String())
	}
	events, err := s.MatchRunEvents(ctx, u, run.ID)
	must(t, err)
	if len(events) < 2 {
		t.Fatal("missing traces", events)
	}
	for _, e := range events {
		if e.RequestID == "" || e.RunID != run.ID {
			t.Fatal("missing correlation", e)
		}
	}
	var stored string
	must(t, s.DB.QueryRowContext(ctx, "SELECT CAST(body AS CHAR) FROM match_runs WHERE id=?", run.ID).Scan(&stored))
	for _, secret := range []string{"api_key", "mask_name", "model_config", "掌握 Go"} {
		if strings.Contains(stored, secret) {
			t.Fatal("outbound data persisted", secret)
		}
	}
	if r := matchingRequest(h, token, "/metrics/prometheus", "GET", nil); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "matching_compare_seconds_bucket") {
		t.Fatal("missing metrics", r.Code, r.Body.String())
	}
}
func TestDurablePauseResumeAndCancellationFence(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	ids := taskJobs(t, ctx, s, u, 4)
	inner := &matchFixtureModel{}
	blocked := &blockingMatchModel{inner: inner, entered: make(chan struct{}), release: make(chan struct{})}
	authn := auth.Service{Store: s, Secret: []byte("durable-pause-integration-secret-32")}
	token, err := authn.Token(u)
	must(t, err)
	a := &transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New(), ResumeModel: blocked, ResumeModelName: "fixture"}
	runner := a.StartMatchTasks(ctx, 1)
	defer runner.Close()
	h := a.Handler()
	preview := matchingRequest(h, token, "/api/matching/preview", "POST", map[string]any{})
	var snap p.MatchSnapshot
	must(t, json.Unmarshal(preview.Body.Bytes(), &snap))
	rec := matchingRequest(h, token, "/api/matching/tasks", "POST", map[string]any{"job_ids": ids, "candidate_hash": snap.CandidateHash, "input_keys": taskInputKeys(snap, ids), "request_key": d.ID()})
	if rec.Code != 202 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var run p.MatchRun
	must(t, json.Unmarshal(rec.Body.Bytes(), &run))
	select {
	case <-blocked.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("not started")
	}
	current, err := s.MatchRun(ctx, u, run.ID)
	must(t, err)
	_, err = s.ControlMatchRun(ctx, u, run.ID, current.Version, "PAUSE")
	must(t, err)
	close(blocked.release)
	paused := awaitRun(t, s, u, run.ID, "PAUSED")
	if inner.calls.Load() != 2 {
		t.Fatal("pause ran later batch", inner.calls.Load())
	}
	pending := []string{}
	for _, i := range paused.Items {
		if i.State == "QUEUED" {
			pending = append(pending, i.JobID)
		}
	}
	if len(pending) != 1 {
		t.Fatal(paused)
	}
	rec = matchingRequest(h, token, "/api/matching/tasks/"+run.ID+"/resume", "POST", map[string]any{"job_ids": pending, "candidate_hash": snap.CandidateHash, "input_keys": taskInputKeys(snap, pending), "version": paused.Version})
	if rec.Code != 202 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	done := awaitRun(t, s, u, run.ID, "COMPLETED")
	if done.Calls != 3 || inner.calls.Load() != 3 {
		t.Fatal("resume did not reuse extraction", done.Calls, inner.calls.Load())
	}
	// A different unfinished run is cancelled while its provider is blocked.
	ids = taskJobs(t, ctx, s, u, 1)
	next := &blockingMatchModel{inner: inner, entered: make(chan struct{}), release: make(chan struct{})}
	a.ResumeModel = next
	preview = matchingRequest(h, token, "/api/matching/preview", "POST", map[string]any{})
	must(t, json.Unmarshal(preview.Body.Bytes(), &snap))
	rec = matchingRequest(h, token, "/api/matching/tasks", "POST", map[string]any{"job_ids": ids, "candidate_hash": snap.CandidateHash, "input_keys": taskInputKeys(snap, ids), "request_key": d.ID()})
	must(t, json.Unmarshal(rec.Body.Bytes(), &run))
	select {
	case <-next.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("not started")
	}
	current, err = s.MatchRun(ctx, u, run.ID)
	must(t, err)
	rec = matchingRequest(h, token, "/api/matching/tasks/"+run.ID+"/control", "POST", map[string]any{"version": current.Version, "action": "CANCEL"})
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	awaitRun(t, s, u, run.ID, "CANCELLED")
	_, err = s.MatchResult(ctx, u, ids[0])
	if !errors.Is(err, p.ErrNotFound) {
		t.Fatal("cancelled model persisted result", err)
	}
}
func TestExpiredTaskRequiresAuthorizationAndResumeOnlySelectedItems(t *testing.T) {
	ctx, s, _, u, _ := setup(t)
	ids := taskJobs(t, ctx, s, u, 3)
	snapshot, err := s.MatchSnapshot(ctx, u, "fixture", "", ids)
	must(t, err)
	items := []p.MatchRunItem{}
	for _, j := range snapshot.Jobs {
		items = append(items, p.MatchRunItem{JobID: j.Job.ID, InputKey: j.InputKey, State: "QUEUED"})
	}
	token := d.ID()
	run, _, err := s.CreateMatchRun(ctx, u, d.ID(), token, p.MatchRun{CandidateHash: snapshot.CandidateHash, Model: "fixture", Items: items, RequestHash: "fixture"})
	must(t, err)
	guard := p.MatchRunGuard{ID: run.ID, Token: token}
	must(t, s.UpdateMatchRun(ctx, u, guard, func(v *p.MatchRun) error { v.Items[0].State = "RUNNING"; return nil }))
	_, err = s.DB.ExecContext(ctx, "UPDATE match_runs SET lease_until=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 1 SECOND) WHERE id=?", run.ID)
	must(t, err)
	_, err = s.MatchRuns(ctx, u)
	must(t, err)
	run, err = s.MatchRun(ctx, u, run.ID)
	must(t, err)
	if run.State != "WAITING_AUTH" || run.Items[0].State != "INTERRUPTED" {
		t.Fatal(run)
	}
	if err = s.SaveMatchResult(p.WithMatchRunGuard(ctx, guard), u, "", matching.Result{JobID: items[0].JobID, InputKey: items[0].InputKey, Model: "fixture"}); !errors.Is(err, p.ErrMatchRunLease) {
		t.Fatal("expired process committed a result", err)
	}
	if err = s.CheckMatchRunGuard(p.WithMatchRunGuard(ctx, guard), u); !errors.Is(err, p.ErrMatchRunLease) {
		t.Fatal("expired executor kept ownership", err)
	}
	_, err = s.ResumeMatchRun(ctx, u, run.ID, d.ID(), run.Version, "changed", "fixture", items[:1])
	if !errors.Is(err, p.ErrStaleInput) {
		t.Fatal("changed input resumed", err)
	}
	resumed, err := s.ResumeMatchRun(ctx, u, run.ID, d.ID(), run.Version, snapshot.CandidateHash, "fixture", items[:1])
	must(t, err)
	if resumed.Items[0].State != "QUEUED" || resumed.Items[1].State != "PAUSED" || resumed.Items[2].State != "PAUSED" {
		t.Fatal("unreviewed jobs resumed", resumed)
	}
	if err = s.UpdateMatchRun(ctx, u, guard, func(v *p.MatchRun) error { v.State = "COMPLETED"; return nil }); !errors.Is(err, p.ErrMatchRunLease) {
		t.Fatal("old process wrote after recovery", err)
	}
}
func TestCampaignQuotaSerializesPlansButPreservesActualApplications(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	ids := taskJobs(t, ctx, s, u, 3)
	job, err := s.JobForUser(ctx, u, ids[0])
	must(t, err)
	rule, err := s.SaveCampaign(ctx, u, "", p.ApplicationCampaign{CompanyID: job.CompanyID, Name: "2027 校招", Limit: 1, JobIDs: ids, Confirmed: true})
	must(t, err)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range ids[:2] {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := s.ApplyAction(ctx, u, d.ID(), "create_application", []byte(d.JSON(p.CreateArgs{JobID: id})))
			results <- err
		}(id)
	}
	wg.Wait()
	close(results)
	success, blocked := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, p.ErrCampaignLimit) {
			blocked++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || blocked != 1 {
		t.Fatal("overbooked quota", success, blocked)
	}
	apps, err := s.ApplicationRecords(ctx, u)
	must(t, err)
	_, err = s.ApplyAction(ctx, u, d.ID(), "transition_application", []byte(d.JSON(p.TransitionArgs{ApplicationID: apps[0].ID, Version: 1, State: "WITHDRAWN"})))
	must(t, err)
	views, err := s.Campaigns(ctx, u)
	must(t, err)
	if views[0].Remaining != 1 {
		t.Fatal("unsubmitted withdrawal kept quota", views)
	}
	authn := auth.Service{Store: s, Secret: []byte("campaign-http-integration-secret-32")}
	token, err := authn.Token(u)
	must(t, err)
	h := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New()}).Handler()
	rec := matchingRequest(h, token, "/api/applications", "POST", p.CreateArgs{JobID: ids[2], Submitted: true})
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var actual d.Application
	must(t, json.Unmarshal(rec.Body.Bytes(), &actual))
	if actual.State != "APPLIED" || actual.AppliedAt == nil {
		t.Fatal(actual)
	}
	_, err = s.ApplyAction(ctx, u, d.ID(), "transition_application", []byte(d.JSON(p.TransitionArgs{ApplicationID: actual.ID, Version: 1, State: "WITHDRAWN"})))
	must(t, err)
	views, err = s.Campaigns(ctx, u)
	must(t, err)
	if views[0].Submitted != 1 || views[0].Remaining != 0 {
		t.Fatal("actual withdrawal restored quota", views)
	}
	free := ids[0]
	if free == apps[0].JobID {
		free = ids[1]
	}
	rec = matchingRequest(h, token, "/api/applications", "POST", p.CreateArgs{JobID: free})
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "CAMPAIGN_LIMIT_REACHED") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = matchingRequest(h, token, "/api/applications", "POST", p.CreateArgs{JobID: free, Submitted: true})
	if rec.Code != 200 {
		t.Fatal("real submitted record lost to plan restriction", rec.Code)
	}
	views, err = s.Campaigns(ctx, u)
	must(t, err)
	if !views[0].Conflict {
		t.Fatal("over-quota record not warned", views)
	}
	other, err := s.NewUser(ctx, d.ID()+"@campaign-owner.invalid", "unused")
	must(t, err)
	if err = s.DeleteCampaign(ctx, other, rule.ID, rule.Version); !errors.Is(err, p.ErrNotFound) {
		t.Fatal("cross-owner delete", err)
	}
	_, err = s.SaveCampaign(ctx, u, rule.ID, p.ApplicationCampaign{CompanyID: job.CompanyID, Name: rule.Name, Limit: 2, JobIDs: ids, Confirmed: true, Version: 99})
	if !errors.Is(err, p.ErrConflict) {
		t.Fatal("stale rule replaced", err)
	}
	must(t, s.DeleteCampaign(ctx, u, rule.ID, rule.Version))
	apps, err = s.ApplicationRecords(ctx, u)
	must(t, err)
	if len(apps) != 3 {
		t.Fatal("rule removal lost application records", apps)
	}
}

func taskInputKeys(s p.MatchSnapshot, ids []string) map[string]string {
	v := map[string]string{}
	for _, id := range ids {
		for _, j := range s.Jobs {
			if j.Job.ID == id {
				v[id] = j.InputKey
			}
		}
	}
	return v
}

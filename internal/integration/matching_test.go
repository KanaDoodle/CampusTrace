package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
)

type matchFixtureModel struct {
	calls       atomic.Int32
	badCitation atomic.Bool
}

type blockingMatchModel struct {
	inner   *matchFixtureModel
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (m *blockingMatchModel) Complete(ctx context.Context, messages, tools any) (json.RawMessage, error) {
	m.once.Do(func() { close(m.entered) })
	select {
	case <-m.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return m.inner.Complete(ctx, messages, tools)
}

func (m *matchFixtureModel) Complete(_ context.Context, messages, _ any) (json.RawMessage, error) {
	m.calls.Add(1)
	var msgs []map[string]string
	if err := json.Unmarshal([]byte(d.JSON(messages)), &msgs); err != nil {
		return nil, err
	}
	if strings.Contains(msgs[1]["content"], "test-private@example.com") {
		return nil, errors.New("sensitive identifier reached model")
	}
	out := []map[string]any{}
	if strings.Contains(msgs[0]["content"], "Extract explicit requirements") {
		var jobs []matching.JobText
		if err := json.Unmarshal([]byte(msgs[1]["content"]), &jobs); err != nil {
			return nil, err
		}
		for _, job := range jobs {
			out = append(out, map[string]any{"job_id": job.ID, "requirements": []matching.Requirement{{ID: "r1", Category: "REQUIRED", Text: "掌握 Go", Excerpt: "Go", Confidence: 1}}})
		}
	} else {
		var input struct {
			Candidate matching.Candidate    `json:"candidate"`
			Jobs      []matching.MatchInput `json:"jobs"`
		}
		if err := json.Unmarshal([]byte(msgs[1]["content"]), &input); err != nil {
			return nil, err
		}
		id := ""
		for _, f := range input.Candidate.Facts {
			if f.Text == "Go" {
				id = f.ID
			}
		}
		excerpt := "Go"
		if m.badCitation.Load() {
			excerpt = "invented Kafka"
		}
		for _, job := range input.Jobs {
			matches := []matching.Match{}
			for _, r := range job.Requirements {
				matches = append(matches, matching.Match{RequirementID: r.ID, Result: "DIRECT", Explanation: "资料明确记录 Go", Evidence: []matching.Citation{{ID: id, Excerpt: excerpt}}})
			}
			out = append(out, map[string]any{"job_id": job.ID, "matches": matches})
		}
	}
	return json.Marshal(map[string]string{"content": d.JSON(map[string]any{"jobs": out})})
}
func matchingRequest(handler http.Handler, token, path, method string, body any) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(d.JSON(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
func TestMatchingBatchCachesAndInvalidatesOnlyPersonalComparison(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{GraduationYear: 2027, Degree: "MASTER", Languages: []string{"Go"}, PreferredTypes: []string{"FULL_TIME"}, TargetRoles: []string{"后端开发"}}))
	ids := []string{}
	for i := 0; i < 2; i++ {
		o, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Synthetic Matching", Title: "服务端开发", JobType: "FULL_TIME", Locations: []string{"上海市"}, ExternalID: d.ID(), Text: "熟悉 Go，开发任务队列，联系 test-private@example.com", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
		must(t, err)
		ids = append(ids, o.JobID)
	}
	authn := auth.Service{Store: s, Secret: []byte("synthetic-matching-http-secret-32")}
	token, err := authn.Token(u)
	must(t, err)
	model := &matchFixtureModel{}
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New(), ResumeModel: model, ResumeModelName: "fixture"}).Handler()
	preview := func() p.MatchSnapshot {
		rec := matchingRequest(handler, token, "/api/matching/preview", "POST", map[string]any{})
		if rec.Code != 200 {
			t.Fatalf("preview %d %s", rec.Code, rec.Body.String())
		}
		var snap p.MatchSnapshot
		must(t, json.Unmarshal(rec.Body.Bytes(), &snap))
		return snap
	}
	analyze := func(hash string) *httptest.ResponseRecorder {
		return matchingRequest(handler, token, "/api/matching/analyze", "POST", map[string]any{"job_ids": ids, "candidate_hash": hash})
	}
	first := preview()
	rec := analyze(first.CandidateHash)
	if rec.Code != 200 {
		t.Fatalf("batch %d %s", rec.Code, rec.Body.String())
	}
	if model.calls.Load() != 2 {
		t.Fatal("identical JD was not deduplicated into one extraction + one comparison", model.calls.Load())
	}
	rec = analyze(first.CandidateHash)
	if rec.Code != 200 || model.calls.Load() != 2 {
		t.Fatal("completed results incurred more calls", rec.Code, model.calls.Load())
	}
	for _, id := range ids {
		result, err := s.MatchResult(ctx, u, id)
		must(t, err)
		if result.Score == nil || *result.Score != 100 {
			t.Fatal("no evidence-based score", result)
		}
	}
	must(t, s.SaveProfile(ctx, u, d.Profile{GraduationYear: 2027, Degree: "MASTER", Languages: []string{"Go"}, Skills: []string{"Redis"}}))
	next := preview()
	if first.CandidateHash == next.CandidateHash {
		t.Fatal("profile change did not invalidate comparison")
	}
	rec = analyze(first.CandidateHash)
	if rec.Code != 409 || model.calls.Load() != 2 {
		t.Fatal("stale review sent to model", rec.Code)
	}
	rec = analyze(next.CandidateHash)
	if rec.Code != 200 || model.calls.Load() != 3 {
		t.Fatal("profile edit reparsed unchanged JD", rec.Code, rec.Body.String(), model.calls.Load())
	}
	other, err := s.NewUser(ctx, d.ID()+"@matching-other.invalid", "unused")
	must(t, err)
	otherToken, err := authn.Token(other)
	must(t, err)
	must(t, s.SaveProfile(ctx, other, d.Profile{Languages: []string{"Go"}}))
	rec = matchingRequest(handler, otherToken, "/api/matching/results/"+ids[0], "POST", map[string]any{})
	if rec.Code != 404 {
		t.Fatal("private match leaked", rec.Code)
	}
	rec = matchingRequest(handler, otherToken, "/api/matching/analyze", "POST", map[string]any{"job_ids": ids, "candidate_hash": "forged"})
	if rec.Code != 404 || model.calls.Load() != 3 {
		t.Fatal("unauthorized job incurred model calls", rec.Code)
	}
	result, err := s.MatchResult(ctx, u, ids[0])
	must(t, err)
	project, err := s.SaveProject(ctx, u, d.Project{Name: "任务队列"})
	must(t, err)
	_, err = s.SaveFact(ctx, u, d.ProjectFact{ProjectID: project.ID, Kind: "IMPLEMENTED", Verified: true, Claim: "实现失败重试"})
	must(t, err)
	if err = s.SaveMatchResult(ctx, u, "", result); !errors.Is(err, p.ErrStaleInput) {
		t.Fatal("fact change accepted stale score", err)
	}
}
func TestMatchingQuotaStopsBeforeCallAndKeepsExtractionForResume(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{Languages: []string{"Go"}}))
	must(t, s.SaveMatchSettings(ctx, u, matching.Settings{RoundLimit: 30, DailyCalls: 1}))
	o, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Synthetic Quota", Title: "Go backend", JobType: "FULL_TIME", Locations: []string{"Shanghai"}, Text: "熟悉 Go", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, err)
	authn := auth.Service{Store: s, Secret: []byte("synthetic-matching-http-secret-32")}
	token, err := authn.Token(u)
	must(t, err)
	model := &matchFixtureModel{}
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New(), ResumeModel: model, ResumeModelName: "fixture"}).Handler()
	snap, err := s.MatchSnapshot(ctx, u, matching.ModelIdentity("server-default", "fixture"), "", []string{o.JobID})
	must(t, err)
	body := map[string]any{"job_ids": []string{o.JobID}, "candidate_hash": snap.CandidateHash}
	rec := matchingRequest(handler, token, "/api/matching/analyze", "POST", body)
	if rec.Code != 429 || !strings.Contains(rec.Body.String(), "MATCH_DAILY_LIMIT") || model.calls.Load() != 1 {
		t.Fatal("quota not enforced before second call", rec.Code, rec.Body.String(), model.calls.Load())
	}
	_, err = s.CachedRequirements(ctx, u, snap.Jobs[0].RequirementsKey)
	must(t, err)
	must(t, s.SaveMatchSettings(ctx, u, matching.Settings{RoundLimit: 30, DailyCalls: 2}))
	rec = matchingRequest(handler, token, "/api/matching/analyze", "POST", body)
	if rec.Code != 200 || model.calls.Load() != 2 {
		t.Fatal("resume lost extraction cache", rec.Code, rec.Body.String())
	}
}
func TestMatchingRejectsInventedCitationsAndRetriesOnlyComparison(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{Languages: []string{"Go"}}))
	o, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Synthetic Match Evidence", Title: "Go backend", JobType: "FULL_TIME", Locations: []string{"Shanghai"}, Text: "熟悉 Go", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, err)
	authn := auth.Service{Store: s, Secret: []byte("synthetic-matching-http-secret-32")}
	token, err := authn.Token(u)
	must(t, err)
	model := &matchFixtureModel{}
	model.badCitation.Store(true)
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New(), ResumeModel: model, ResumeModelName: "fixture"}).Handler()
	snap, err := s.MatchSnapshot(ctx, u, matching.ModelIdentity("server-default", "fixture"), "", []string{o.JobID})
	must(t, err)
	body := map[string]any{"job_ids": []string{o.JobID}, "candidate_hash": snap.CandidateHash}
	rec := matchingRequest(handler, token, "/api/matching/analyze", "POST", body)
	if rec.Code != 502 {
		t.Fatal("invented citation accepted", rec.Code)
	}
	if _, err = s.MatchResult(ctx, u, o.JobID); !errors.Is(err, p.ErrNotFound) {
		t.Fatal("invalid score persisted", err)
	}
	model.badCitation.Store(false)
	rec = matchingRequest(handler, token, "/api/matching/analyze", "POST", body)
	if rec.Code != 200 || model.calls.Load() != 3 {
		t.Fatal("failed comparison unnecessarily reparsed JD", rec.Code, rec.Body.String(), model.calls.Load())
	}
}
func TestMatchingDailyLimitIsAtomic(t *testing.T) {
	ctx, s, _, u, _ := setup(t)
	must(t, s.SaveMatchSettings(ctx, u, matching.Settings{RoundLimit: 30, DailyCalls: 1}))
	var success atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.ReserveMatchCall(ctx, u, time.Now())
			if err == nil {
				success.Add(1)
			} else if !errors.Is(err, p.ErrMatchQuota) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if success.Load() != 1 {
		t.Fatal("concurrent calls exceeded limit", success.Load())
	}
}

func TestMatchingLeasePreventsDuplicatePaidBatches(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{Languages: []string{"Go"}}))
	o, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Synthetic Match Lease", Title: "Go backend", JobType: "FULL_TIME", Locations: []string{"Shanghai"}, Text: "熟悉 Go", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, err)
	authn := auth.Service{Store: s, Secret: []byte("synthetic-matching-http-secret-32")}
	token, err := authn.Token(u)
	must(t, err)
	model := &blockingMatchModel{inner: &matchFixtureModel{}, entered: make(chan struct{}), release: make(chan struct{})}
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New(), ResumeModel: model, ResumeModelName: "fixture"}).Handler()
	snap, err := s.MatchSnapshot(ctx, u, matching.ModelIdentity("server-default", "fixture"), "", []string{o.JobID})
	must(t, err)
	body := map[string]any{"job_ids": []string{o.JobID}, "candidate_hash": snap.CandidateHash}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- matchingRequest(handler, token, "/api/matching/analyze", "POST", body) }()
	select {
	case <-model.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	second := matchingRequest(handler, token, "/api/matching/analyze", "POST", body)
	close(model.release)
	first := <-done
	if second.Code != 409 || !strings.Contains(second.Body.String(), "MATCH_BUSY") || first.Code != 200 || model.inner.calls.Load() != 2 {
		t.Fatal("duplicate paid work was not prevented", first.Code, second.Code, model.inner.calls.Load())
	}
}

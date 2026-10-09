package integration

import (
	"context"
	"encoding/json"
	"errors"
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

type limitationFixtureModel struct {
	inner   matchFixtureModel
	forge   bool
	correct bool
}

func (m *limitationFixtureModel) Complete(ctx context.Context, messages, tools any) (json.RawMessage, error) {
	raw, err := m.inner.Complete(ctx, messages, tools)
	if err != nil || m.correct {
		return raw, err
	}
	var msgs []map[string]string
	if err := json.Unmarshal([]byte(d.JSON(messages)), &msgs); err != nil {
		return nil, err
	}
	var input matching.HolisticRequest
	if err := json.Unmarshal([]byte(msgs[1]["content"]), &input); err != nil {
		return nil, err
	}
	var limit matching.Fact
	for _, f := range fixtureDocumentFacts(input.Candidate) {
		if f.Kind == "LIMITATION" {
			limit = f
			break
		}
	}
	if limit.ID == "" {
		return nil, errors.New("missing complete limitation")
	}
	excerpt := limit.Text
	if m.forge {
		excerpt = "生产环境性能验证"
	}
	jobs := []matching.HolisticJobReply{}
	for _, j := range input.Jobs {
		jobs = append(jobs, matching.HolisticJobReply{ID: j.ID, Assessment: wholeAssessment(j.Text, []matching.Citation{{ID: limit.ID, Excerpt: excerpt}})})
	}
	return json.Marshal(map[string]string{"content": d.JSON(map[string]any{"jobs": jobs})})
}

func TestWholeMatchingRejectsFabricatedAndMisusedEvidenceWithoutAutomaticCalls(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{GraduationYear: 2027, Degree: "MASTER", Languages: []string{"Go"}, TargetRoles: []string{"后端开发"}, PreferredCities: []string{"上海"}}))
	project, err := s.SaveProject(ctx, u, d.Project{Name: "证据核对测试项目"})
	must(t, err)
	_, err = s.SaveFact(ctx, u, d.ProjectFact{ProjectID: project.ID, Kind: "LIMITATION", Verified: true, Claim: "尚无生产环境经验"})
	must(t, err)
	ids := []string{}
	for i := 0; i < 3; i++ {
		o, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Evidence fixture", Title: "服务端开发", JobType: "FULL_TIME", ExternalID: d.ID(), Text: "熟悉 Go", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
		must(t, err)
		ids = append(ids, o.JobID)
	}
	model := &limitationFixtureModel{forge: true}
	authn := auth.Service{Store: s, Secret: []byte("evidence-fixture-http-secret-32bytes")}
	token, err := authn.Token(u)
	must(t, err)
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New(), ResumeModel: model, ResumeModelName: "fixture"}).Handler()
	snap, err := s.MatchSnapshot(ctx, u, matching.ModelIdentity("server-default", "fixture"), "", ids)
	must(t, err)
	body := map[string]any{"job_ids": ids, "candidate_hash": snap.CandidateHash}
	rec := matchingRequest(handler, token, "/api/matching/analyze", "POST", body)
	if rec.Code != 502 || model.inner.calls.Load() != 1 {
		t.Fatal("forged excerpt accepted", rec.Code, rec.Body.String())
	}
	for _, id := range ids {
		_, err := s.MatchResult(ctx, u, id)
		if !errors.Is(err, p.ErrNotFound) {
			t.Fatal("forged result persisted", err)
		}
	}
	model.forge = false
	rec = matchingRequest(handler, token, "/api/matching/analyze", "POST", body)
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), "INVALID_ABILITY_EVIDENCE") || model.inner.calls.Load() != 2 {
		t.Fatal("limitation counted as ability", rec.Code, rec.Body.String())
	}
	for _, id := range ids {
		if _, err := s.MatchResult(ctx, u, id); !errors.Is(err, p.ErrNotFound) {
			t.Fatal("invalid proof saved", err)
		}
	}
	model.correct = true
	rec = matchingRequest(handler, token, "/api/matching/analyze", "POST", body)
	if rec.Code != 200 || model.inner.calls.Load() != 3 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	for _, id := range ids {
		r, err := s.MatchResult(ctx, u, id)
		must(t, err)
		if r.Score != nil || r.Holistic == nil || len(r.Holistic.Strengths) != 1 || r.Holistic.Strengths[0].Evidence[0].ID != "language-0" {
			t.Fatal("correct proof lost", r)
		}
	}
	rec = matchingRequest(handler, token, "/api/matching/analyze", "POST", body)
	if rec.Code != 200 || model.inner.calls.Load() != 3 {
		t.Fatal("cache repeated a paid call", rec.Code, rec.Body.String())
	}
	current, err := s.MatchSnapshot(ctx, u, matching.ModelIdentity("server-default", "fixture"), "", ids)
	must(t, err)
	if current.CallsToday != 3 {
		t.Fatal("unexpected quota", current.CallsToday)
	}
}

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
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
)

type projectExcerptFixtureModel struct {
	calls   int
	invalid bool
}

func (m *projectExcerptFixtureModel) Complete(_ context.Context, messages, _ any) (json.RawMessage, error) {
	m.calls++
	var msgs []map[string]string
	if err := json.Unmarshal([]byte(d.JSON(messages)), &msgs); err != nil {
		return nil, err
	}
	if strings.Contains(msgs[0]["content"], "Extract explicit requirements") {
		return nil, errors.New("cached requirements must not be re-extracted")
	}
	var input matching.HolisticRequest
	if err := json.Unmarshal([]byte(msgs[1]["content"]), &input); err != nil {
		return nil, err
	}
	var selected matching.Fact
	for _, f := range fixtureDocumentFacts(input.Candidate) {
		if f.Kind == "IMPLEMENTED" {
			selected = f
			break
		}
	}
	if !strings.Contains(selected.Text, strings.Repeat("通过 Redis Streams 实现任务重试。\n", 20)) {
		return nil, errors.New("complete project fact was not provided")
	}
	excerpt := "通过 Redis Streams 实现任务重试。"
	if m.invalid {
		excerpt = "以 Kafka 实现高可用生产队列"
	}
	evidence := []matching.Citation{{ID: selected.ID, Excerpt: excerpt}}
	jobs := []matching.HolisticJobReply{}
	for _, j := range input.Jobs {
		jobs = append(jobs, matching.HolisticJobReply{ID: j.ID, Assessment: wholeAssessment(j.Text, evidence)})
	}
	return json.Marshal(map[string]string{"content": d.JSON(map[string]any{"jobs": jobs})})
}

func TestMatchingExcerptRetryReusesRequirementsAndPersistsOnlyOriginalProjectText(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{Languages: []string{"Go"}}))
	project, err := s.SaveProject(ctx, u, d.Project{Name: "原文引用测试项目"})
	must(t, err)
	source := strings.Repeat("通过 Redis Streams 实现任务重试。\n", 20)
	fact, err := s.SaveFact(ctx, u, d.ProjectFact{ProjectID: project.ID, Kind: "IMPLEMENTED", Claim: source, Verified: true})
	must(t, err)
	o, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Excerpt synthetic", Title: "服务端开发", JobType: "FULL_TIME", Text: "具备任务重试实现经验", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, err)
	model := &projectExcerptFixtureModel{invalid: true}
	authn := auth.Service{Store: s, Secret: []byte("excerpt-fixture-http-secret-at-least-32")}
	token, err := authn.Token(u)
	must(t, err)
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, ResumeModel: model, ResumeModelName: "fixture"}).Handler()
	snap, err := s.MatchSnapshot(ctx, u, matching.ModelIdentity("server-default", "fixture"), "", []string{o.JobID})
	must(t, err)
	reqs := []matching.Requirement{{ID: "retry", Category: "REQUIRED", Text: "任务重试实现经验", Excerpt: "任务重试实现经验", Confidence: 1}}
	must(t, s.SaveRequirements(ctx, u, snap.Jobs[0].RequirementsKey, matching.Requirements{Items: reqs}))
	body := map[string]any{"job_ids": []string{o.JobID}, "candidate_hash": snap.CandidateHash}
	rec := matchingRequest(handler, token, "/api/matching/analyze", "POST", body)
	if rec.Code != 502 || model.calls != 1 || !strings.Contains(rec.Body.String(), "EXCERPT_NOT_CONTIGUOUS") || strings.Contains(rec.Body.String(), "Kafka") {
		t.Fatal("paraphrased quote was accepted, retried or echoed", rec.Code, rec.Body.String())
	}
	_, err = s.MatchResult(ctx, u, o.JobID)
	if !errors.Is(err, p.ErrNotFound) {
		t.Fatal("failed comparison wrote an unverified result", err)
	}
	model.invalid = false
	rec = matchingRequest(handler, token, "/api/matching/analyze", "POST", body)
	var response struct {
		Calls              int `json:"calls"`
		RequirementsReused int `json:"requirements_reused"`
	}
	must(t, json.Unmarshal(rec.Body.Bytes(), &response))
	if rec.Code != 200 || response.Calls != 1 || response.RequirementsReused != 0 || model.calls != 2 {
		t.Fatal("explicit retry lost its cached requirements", rec.Code, rec.Body.String())
	}
	result, err := s.MatchResult(ctx, u, o.JobID)
	must(t, err)
	quote := result.Holistic.Strengths[0].Evidence[0]
	if quote.ID != fact.ID || quote.Excerpt == "" || len(quote.Excerpt) > 600 || !strings.Contains(source, quote.Excerpt) || result.Score != nil || matching.ValidateHolistic(*result.Holistic, snap.Jobs[0].Text, snap.Candidate) != nil {
		t.Fatal("stored quote is not a bounded original source span")
	}
	found := false
	for _, f := range result.CandidateFacts {
		if f.ID == fact.ID {
			found = true
			if f.Text != source {
				t.Fatal("saved project text was split or modified")
			}
		}
	}
	if !found {
		t.Fatal("source fact disappeared")
	}
	rec = matchingRequest(handler, token, "/api/matching/analyze", "POST", body)
	if rec.Code != 200 || model.calls != 2 {
		t.Fatal("verified result stopped being reusable", rec.Code, rec.Body.String())
	}
}

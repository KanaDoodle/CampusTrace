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
	inner matchFixtureModel
	forge bool
}

func (m *limitationFixtureModel) Complete(ctx context.Context, messages, tools any) (json.RawMessage, error) {
	raw, err := m.inner.Complete(ctx, messages, tools)
	if err != nil {
		return nil, err
	}
	var msgs []map[string]string
	if err := json.Unmarshal([]byte(d.JSON(messages)), &msgs); err != nil {
		return nil, err
	}
	if strings.Contains(msgs[0]["content"], "Extract explicit requirements") {
		return raw, nil
	}
	var input struct {
		Candidate matchFixtureCandidate `json:"candidate"`
		Jobs      []matching.MatchInput `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(msgs[1]["content"]), &input); err != nil {
		return nil, err
	}
	if len(input.Candidate.Limitations) != 1 {
		return nil, errors.New("fixture expects separate limitation")
	}
	for _, fact := range input.Candidate.Facts {
		if fact.Kind == "LIMITATION" || fact.Kind == "ROLE" || fact.Kind == "CITY_PREFERRED" {
			return nil, errors.New("input facts mix preferences and abilities")
		}
	}
	f := input.Candidate.Limitations[0]
	excerpt := f.text()
	if m.forge {
		excerpt = "生产环境性能验证"
	}
	jobs := []any{}
	for _, j := range input.Jobs {
		matches := []matching.Match{}
		for _, r := range j.Requirements {
			matches = append(matches, matching.Match{RequirementID: r.ID, Result: "DIRECT", Explanation: "错误地由局限推断能力", Evidence: []matching.Citation{{ID: f.ID, Excerpt: excerpt}}})
		}
		jobs = append(jobs, map[string]any{"job_id": j.ID, "matches": matches})
	}
	return json.Marshal(map[string]string{"content": d.JSON(map[string]any{"jobs": jobs})})
}

func TestMatchingKnownEvidenceMisusePersistsReviewWithoutRepeatingExtractionOrCalls(t *testing.T) {
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
	if rec.Code != 502 || model.inner.calls.Load() != 2 {
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
	var response struct {
		Analyzed           []string `json:"analyzed"`
		Reused             []string `json:"reused"`
		Calls              int      `json:"calls"`
		RequirementsReused int      `json:"requirements_reused"`
		EvidenceReviews    int      `json:"evidence_reviews"`
	}
	must(t, json.Unmarshal(rec.Body.Bytes(), &response))
	if rec.Code != 200 || len(response.Analyzed) != 3 || response.Calls != 1 || response.RequirementsReused != 3 || response.EvidenceReviews != 3 || model.inner.calls.Load() != 3 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	for _, id := range ids {
		r, err := s.MatchResult(ctx, u, id)
		must(t, err)
		if r.Score != nil || r.Coverage != 0 || r.Matches[0].Result != "NO_EVIDENCE" || len(r.Matches[0].Evidence) != 0 || r.Matches[0].ReviewNote != matching.InvalidAbilityEvidence {
			t.Fatal("invalid proof counted as ability", r)
		}
		rec := matchingRequest(handler, token, "/api/matching/results/"+id, "POST", map[string]any{})
		if rec.Code != 200 {
			t.Fatal(rec.Code)
		}
		var result struct {
			State  string          `json:"state"`
			Result matching.Result `json:"result"`
		}
		must(t, json.Unmarshal(rec.Body.Bytes(), &result))
		if result.State != "ANALYZED" || matching.EvidenceReviewCount(result.Result.Matches) != 1 {
			t.Fatal(result)
		}
		rec = matchingRequest(handler, token, "/api/matching/preparation/"+id, "POST", map[string]any{})
		var plan matching.PreparationPlan
		must(t, json.Unmarshal(rec.Body.Bytes(), &plan))
		if rec.Code != 200 || plan.EvidenceReviews != 1 || plan.Score != nil {
			t.Fatal(rec.Code, plan)
		}
		for _, task := range plan.Tasks {
			if task.Category == "REQUIRED" && (task.Kind != "EVIDENCE" || len(task.Evidence) != 0) {
				t.Fatal(task)
			}
		}
	}
	rec = matchingRequest(handler, token, "/api/matching/company", "POST", map[string]any{"company": "Evidence fixture"})
	var report matching.CompanyComparison
	must(t, json.Unmarshal(rec.Body.Bytes(), &report))
	if rec.Code != 200 || report.Recommendation != "NONE" || report.Analyzed != 3 {
		t.Fatal(rec.Code, report)
	}
	for _, row := range report.Jobs {
		if row.EvidenceReviews != 1 || row.Score != nil || row.Recommended {
			t.Fatal(row)
		}
	}
	rec = matchingRequest(handler, token, "/api/matching/analyze", "POST", body)
	must(t, json.Unmarshal(rec.Body.Bytes(), &response))
	if rec.Code != 200 || len(response.Reused) != 3 || response.Calls != 0 || model.inner.calls.Load() != 3 {
		t.Fatal("saved reviews caused another paid call", rec.Code, rec.Body.String())
	}
	current, err := s.MatchSnapshot(ctx, u, matching.ModelIdentity("server-default", "fixture"), "", ids)
	must(t, err)
	if current.CandidateHash != snap.CandidateHash || current.CallsToday != 3 {
		t.Fatal("facts changed or unexpected quota use")
	}
}

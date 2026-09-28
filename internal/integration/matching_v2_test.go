package integration

import (
	"context"
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

type preferenceMatchModel struct{ calls int }

func (m *preferenceMatchModel) Complete(_ context.Context, messages, _ any) (json.RawMessage, error) {
	m.calls++
	var msgs []map[string]string
	if err := json.Unmarshal([]byte(d.JSON(messages)), &msgs); err != nil {
		return nil, err
	}
	jobs := []map[string]any{}
	if strings.Contains(msgs[0]["content"], "Extract explicit requirements") {
		var input []matching.JobText
		if err := json.Unmarshal([]byte(msgs[1]["content"]), &input); err != nil {
			return nil, err
		}
		for _, j := range input {
			jobs = append(jobs, map[string]any{"job_id": j.ID, "requirements": []matching.Requirement{
				{ID: "go", Category: "REQUIRED", Text: "熟悉 Go", Excerpt: "熟悉 Go", Confidence: 1},
				{ID: "city", Category: "QUALIFICATION", Text: "工作地点：上海市、杭州市", Excerpt: "工作地点：上海市、杭州市", ClaimType: "LOCATION", Value: "上海市|杭州市", Confidence: 1},
				{ID: "soft", Category: "REQUIRED", Text: "沟通协作顺畅", Excerpt: "沟通协作顺畅", Confidence: 1},
				{ID: "bonus", Category: "QUALIFICATION", Text: "软件工程专业优先", Excerpt: "软件工程专业优先", ClaimType: "MAJOR_REQUIREMENT", Value: "软件工程", Confidence: 1},
			}})
		}
	} else {
		var input struct {
			Candidate matching.Candidate    `json:"candidate"`
			Jobs      []matching.MatchInput `json:"jobs"`
		}
		if err := json.Unmarshal([]byte(msgs[1]["content"]), &input); err != nil {
			return nil, err
		}
		for _, j := range input.Jobs {
			matches := []matching.Match{}
			for _, r := range j.Requirements {
				match := matching.Match{RequirementID: r.ID, Result: "NO_EVIDENCE", Explanation: "资料中暂无依据", Evidence: []matching.Citation{}}
				if r.Text == "熟悉 Go" {
					for _, f := range input.Candidate.Facts {
						if f.Kind == "LANGUAGE" && f.Text == "Go" {
							match.Result = "DIRECT"
							match.Explanation = "语言有依据"
							match.Evidence = []matching.Citation{{ID: f.ID, Excerpt: f.Text}}
						}
					}
				}
				matches = append(matches, match)
			}
			jobs = append(jobs, map[string]any{"job_id": j.ID, "matches": matches})
		}
	}
	return json.Marshal(map[string]string{"content": d.JSON(map[string]any{"jobs": jobs})})
}

func TestMatchingV2PreferencesSectionsProjectContextAndStaleResults(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{GraduationYear: 2027, Degree: "MASTER", Languages: []string{"Go"}, PreferredCities: []string{"Shanghai"}, PreferredTypes: []string{"FULL_TIME"}, Majors: []string{"数学"}}))
	project, err := s.SaveProject(ctx, u, d.Project{Name: "任务队列"})
	must(t, err)
	_, err = s.SaveFact(ctx, u, d.ProjectFact{ProjectID: project.ID, Kind: "IMPLEMENTED", Verified: true, Claim: "使用 SQL 行锁调度任务"})
	must(t, err)
	o, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Synthetic matching v2", Title: "Go 平台开发", JobType: "FULL_TIME", Locations: []string{"上海市"}, Text: "熟悉 Go\n工作地点：上海市、杭州市\n沟通协作顺畅\n软件工程专业优先", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, err)
	authn := auth.Service{Store: s, Secret: []byte("synthetic-matching-v2-http-secret-32")}
	token, err := authn.Token(u)
	must(t, err)
	model := &preferenceMatchModel{}
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New(), ResumeModel: model, ResumeModelName: "fixture"}).Handler()
	identity := matching.ModelIdentity("server-default", "fixture")
	snap, err := s.MatchSnapshot(ctx, u, identity, "", []string{o.JobID})
	must(t, err)
	rec := matchingRequest(handler, token, "/api/matching/analyze", "POST", map[string]any{"job_ids": []string{o.JobID}, "candidate_hash": snap.CandidateHash})
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	result, err := s.MatchResult(ctx, u, o.JobID)
	must(t, err)
	if result.Score == nil || *result.Score != 100 || result.Coverage != 100 || len(result.Breakdown) != 4 || result.Breakdown[3].Total != 1 || result.Breakdown[2].Missing != 1 {
		t.Fatal(result)
	}
	for i, r := range result.Requirements {
		if r.ClaimType == "LOCATION" && (result.Matches[i].Result != "DIRECT" || result.Matches[i].Evidence[0].Excerpt != "Shanghai") {
			t.Fatal("model's missing-city claim survived", result.Matches[i])
		}
	}
	for _, row := range result.Qualifications.Results {
		if row.Rule == "LOCATION" && row.Result != "PASS" {
			t.Fatal(row)
		}
		if row.Rule == "MAJOR_REQUIREMENT" && row.Result != "NOT_APPLICABLE" {
			t.Fatal("preferred major became gate", row)
		}
	}
	project.Name = "任务调度平台"
	_, err = s.UpdateProject(ctx, u, project.ID, project)
	must(t, err)
	changed, err := s.MatchSnapshot(ctx, u, identity, "", []string{o.JobID})
	must(t, err)
	if changed.CandidateHash == snap.CandidateHash || changed.Jobs[0].State != "STALE" || changed.Jobs[0].RequirementsKey != snap.Jobs[0].RequirementsKey {
		t.Fatal("project rename did not invalidate only personal comparison", changed)
	}
	if err := s.SaveMatchResult(ctx, u, "", result); err != p.ErrStaleInput {
		t.Fatal("stale project context was saved", err)
	}
	rec = matchingRequest(handler, token, "/api/matching/results/"+o.JobID, "POST", map[string]any{})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"state":"STALE"`) || !strings.Contains(rec.Body.String(), `"project_name":"任务队列"`) || model.calls != 2 {
		t.Fatal("prior result erased or read triggered paid analysis", rec.Code, rec.Body.String(), model.calls)
	}
	// A prior-version result must not be reused even if JD and profile are unchanged.
	result.InputKey = matching.InputKey(d.Hash("matching-v1\n"+identity+"\n"+changed.Jobs[0].Text), changed.CandidateHash)
	_, err = s.DB.ExecContext(ctx, "UPDATE job_match_results SET body=? WHERE user_id=? AND job_id=?", d.JSON(result), u, o.JobID)
	must(t, err)
	versioned, err := s.MatchSnapshot(ctx, u, identity, "", []string{o.JobID})
	must(t, err)
	if versioned.Jobs[0].State != "STALE" || model.calls != 2 {
		t.Fatal("old version consumed or silently refreshed", versioned.Jobs, model.calls)
	}
}

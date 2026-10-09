package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
)

type wholeModel struct {
	calls atomic.Int32
	bad   atomic.Bool
}

func (m *wholeModel) Complete(_ context.Context, messages, _ any) (json.RawMessage, error) {
	m.calls.Add(1)
	var msgs []map[string]string
	if err := json.Unmarshal([]byte(d.JSON(messages)), &msgs); err != nil {
		return nil, err
	}
	if strings.Contains(msgs[1]["content"], "private-whole@example.com") {
		return nil, errors.New("unredacted candidate reached provider")
	}
	var input matching.HolisticRequest
	if err := json.Unmarshal([]byte(msgs[1]["content"]), &input); err != nil {
		return nil, err
	}
	var project matching.Fact
	for _, f := range fixtureDocumentFacts(input.Candidate) {
		if strings.HasSuffix(f.ID, "-description") {
			project = f
			break
		}
	}
	if project.ID == "" || !strings.Contains(input.Candidate.Document, "保留完整背景、实现方法与结果，不拆句。") {
		return nil, errors.New("complete saved project missing")
	}
	evidence := []matching.Citation{{ID: project.ID, Excerpt: project.Text}}
	if m.bad.Load() {
		evidence[0].Excerpt = "invented production scale"
	}
	if strings.Contains(msgs[0]["content"], "company comparison") {
		choices := []matching.CompanyChoice{}
		for i, j := range input.Jobs {
			choices = append(choices, matching.CompanyChoice{ID: j.ID, Rank: i + 1, Reason: "核心工作与项目服务开发相关", Advantage: "可迁移事务与重试经验", Tradeoff: "需要确认业务规模与领域知识", JobExcerpt: j.Text, Evidence: evidence})
		}
		return json.Marshal(map[string]string{"content": d.JSON(map[string]any{"summary": "按本次候选岗位核心工作比较，不按技术名词计数", "choices": choices, "questions": []string{"确认公司实际限投规则"}})})
	}
	jobs := []matching.HolisticJobReply{}
	for _, j := range input.Jobs {
		jobs = append(jobs, matching.HolisticJobReply{ID: j.ID, Assessment: wholeAssessment(j.Text, evidence)})
	}
	return json.Marshal(map[string]string{"content": d.JSON(map[string]any{"jobs": jobs})})
}
func wholeAssessment(source string, evidence []matching.Citation) matching.HolisticAssessment {
	return matching.HolisticAssessment{Version: matching.HolisticVersion, Fit: "RELATED", Summary: "后端服务实践相关，可以考虑投递", CoreWork: "开发后端服务并处理可靠性问题", Strengths: []matching.HolisticFinding{{Point: "工程经验可以迁移", Explanation: "完整项目具有事务与重试处理，不虚构线上规模", JobExcerpt: source, Evidence: evidence}}, Gaps: []matching.HolisticFinding{}, Blockers: []matching.HolisticFinding{}, Questions: []string{"领域知识与规模需要确认"}, NextSteps: []string{"准备讲解任务处理与故障恢复"}, IgnoredFactors: []string{"热爱技术"}, Gates: []matching.HolisticGate{}}
}
func wholeSetup(t *testing.T) (context.Context, *p.Store, string, string, []string, *transport.API, http.Handler, *wholeModel) {
	t.Helper()
	ctx, s, q, u, _ := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{Languages: []string{"Go"}, TargetRoles: []string{"后端开发"}, Degree: "MASTER", GraduationYear: 2027}))
	_, err := s.SaveProject(ctx, u, d.Project{Name: "任务服务", Description: "张小明使用 Go 实现任务服务，通过事务和重试处理失败。", Bullets: []string{"保留完整背景、实现方法与结果，不拆句。"}})
	must(t, err)
	// Older local records can predate the contact filter on project writes.
	var project d.Project
	project, err = p.One[d.Project](ctx, s.DB, "SELECT body FROM projects WHERE user_id=?", u)
	must(t, err)
	project.Description += " 联系 private-whole@example.com"
	_, err = s.DB.ExecContext(ctx, "UPDATE projects SET body=? WHERE id=?", d.JSON(project), project.ID)
	must(t, err)
	ids := []string{}
	for _, title := range []string{"服务端开发", "基础平台开发"} {
		o, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Whole Synthetic", Title: title, JobType: "FULL_TIME", ExternalID: d.ID(), Locations: []string{"上海市"}, Text: "任职要求：熟悉Go。工作职责：开发后端服务并处理可靠性问题。热爱技术。", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
		must(t, err)
		ids = append(ids, o.JobID)
	}
	authn := auth.Service{Store: s, Secret: []byte("whole-fixture-at-least-32-byte-secret")}
	token, err := authn.Token(u)
	must(t, err)
	model := &wholeModel{}
	api := &transport.API{Store: s, Queue: q, Auth: authn, ResumeModel: model, ResumeModelName: "whole-fixture"}
	return ctx, s, u, token, ids, api, api.Handler(), model
}
func TestWholeAnalysisSavesNarrativeReportAndInvalidatesOnProjectEdit(t *testing.T) {
	ctx, s, u, token, ids, _, h, m := wholeSetup(t)
	identity := matching.ModelIdentity("server-default", "whole-fixture")
	snap, err := s.MatchSnapshot(ctx, u, identity, "", ids)
	must(t, err)
	body := map[string]any{"job_ids": ids, "candidate_hash": snap.CandidateHash}
	rec := matchingRequest(h, token, "/api/matching/analyze", "POST", body)
	if rec.Code != 200 || m.calls.Load() != 1 {
		t.Fatal(rec.Code, rec.Body.String(), m.calls.Load())
	}
	for _, id := range ids {
		r, err := s.MatchResult(ctx, u, id)
		must(t, err)
		if r.Holistic == nil || r.Score != nil || len(r.Matches) != 0 {
			t.Fatal("whole report still counted requirements", r)
		}
	}
	rec = matchingRequest(h, token, "/api/matching/analyze", "POST", body)
	if rec.Code != 200 || m.calls.Load() != 1 {
		t.Fatal("cache hit incurred a call", rec.Code, m.calls.Load())
	}
	project := snap.Candidate.Projects[0]
	_, err = s.UpdateProject(ctx, u, project.ID, d.Project{ID: project.ID, Name: project.Name, Description: project.Description + "新增故障恢复细节", Bullets: []string{project.Bullets[0].Text}})
	must(t, err)
	next, err := s.MatchSnapshot(ctx, u, identity, "", ids)
	must(t, err)
	if next.CandidateHash == snap.CandidateHash || next.Jobs[0].State != "STALE" {
		t.Fatal("project prose did not invalidate analysis")
	}
	rec = matchingRequest(h, token, "/api/matching/analyze", "POST", body)
	if rec.Code != 409 || m.calls.Load() != 1 {
		t.Fatal("stale review reached model", rec.Code, m.calls.Load())
	}
}
func TestCompanyWholeTaskWorksBeforeSingleAnalysisAndCachesExactScope(t *testing.T) {
	ctx, s, u, token, ids, api, h, m := wholeSetup(t)
	runner := api.StartMatchTasks(ctx, 1)
	t.Cleanup(runner.Close)
	identity := matching.ModelIdentity("server-default", "whole-fixture")
	snap, err := s.MatchSnapshot(ctx, u, identity, "", ids)
	must(t, err)
	jobs := p.WholeJobs(snap.Jobs)
	key := matching.CompanyInputKey(snap.Candidate, jobs, identity)
	body := map[string]any{"kind": "COMPANY", "company": "Whole Synthetic", "scope_key": key, "job_ids": ids, "input_keys": taskInputKeys(snap, ids), "candidate_hash": snap.CandidateHash, "request_key": d.ID()}
	rec := matchingRequest(h, token, "/api/matching/tasks", "POST", body)
	if rec.Code != 202 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var run p.MatchRun
	must(t, json.Unmarshal(rec.Body.Bytes(), &run))
	done := awaitRun(t, s, u, run.ID, "COMPLETED")
	if done.Kind != "COMPANY" || done.Calls != 1 || m.calls.Load() != 1 {
		t.Fatal(done, m.calls.Load())
	}
	report, err := s.CompanyReport(ctx, u, "Whole Synthetic", key)
	must(t, err)
	if len(report.Choices) != 2 || report.AnalyzedAt == "" {
		t.Fatal(report)
	}
	for _, id := range ids {
		if _, err := s.MatchResult(ctx, u, id); !errors.Is(err, p.ErrNotFound) {
			t.Fatal("company task rewrote individual reports", err)
		}
	}
	rec = matchingRequest(h, token, "/api/matching/company", "POST", map[string]any{"company": "Whole Synthetic", "scope": "SELECTED", "job_ids": ids})
	var comparison matching.CompanyComparison
	must(t, json.Unmarshal(rec.Body.Bytes(), &comparison))
	if rec.Code != 200 || comparison.Holistic == nil || m.calls.Load() != 1 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	body["request_key"] = d.ID()
	rec = matchingRequest(h, token, "/api/matching/tasks", "POST", body)
	if rec.Code != 202 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	must(t, json.Unmarshal(rec.Body.Bytes(), &run))
	done = awaitRun(t, s, u, run.ID, "COMPLETED")
	if done.Calls != 0 || m.calls.Load() != 1 {
		t.Fatal("company cache missed", done.Calls, m.calls.Load())
	}
	// Simulate restart after the report committed but only one item's success
	// marker was persisted. Recovery must revisit the whole scope via cache.
	done.State = "COMPLETED_WITH_ERRORS"
	done.Items[0].State = "SUCCEEDED"
	done.Items[1].State = "INTERRUPTED"
	_, err = s.DB.ExecContext(ctx, "UPDATE match_runs SET state=?,body=?,token='',lease_until=NULL WHERE id=?", done.State, d.JSON(done), done.ID)
	must(t, err)
	body["version"] = done.Version
	body["retry"] = true
	rec = matchingRequest(h, token, "/api/matching/tasks/"+done.ID+"/resume", "POST", body)
	if rec.Code != 202 {
		t.Fatal("partial company recovery rejected", rec.Code, rec.Body.String())
	}
	recovered := awaitRun(t, s, u, done.ID, "COMPLETED")
	if recovered.Calls != 0 || m.calls.Load() != 1 || len(recovered.Items) != 2 {
		t.Fatal("recovery narrowed scope or repeated model call", recovered.Calls, m.calls.Load())
	}
	rec = matchingRequest(h, token, "/api/matching/company", "POST", map[string]any{"company": "Whole Synthetic", "scope": "SELECTED", "job_ids": ids[:1]})
	narrowed := matching.CompanyComparison{}
	must(t, json.Unmarshal(rec.Body.Bytes(), &narrowed))
	if rec.Code != 200 || narrowed.Holistic != nil {
		t.Fatal("ranking survived changed candidate set", rec.Code)
	}
	// A late report must not restore an ignored job to the comparison scope.
	must(t, s.SetPreference(ctx, u, ids[0], "IGNORED"))
	if err := s.SaveCompanyReport(ctx, u, "", report, jobs); !errors.Is(err, p.ErrStaleInput) {
		t.Fatal("ignored job committed", err)
	}
	must(t, s.SetPreference(ctx, u, ids[0], "NONE"))
	original, err := s.JobForUser(ctx, u, ids[0])
	must(t, err)
	edited := original
	edited.Title += "-变更职责名称"
	_, err = s.DB.ExecContext(ctx, "UPDATE jobs SET body=? WHERE id=?", d.JSON(edited), edited.ID)
	must(t, err)
	if err := s.SaveCompanyReport(ctx, u, "", report, jobs); !errors.Is(err, p.ErrStaleInput) {
		t.Fatal("metadata change lost commit fence", err)
	}
	_, err = s.DB.ExecContext(ctx, "UPDATE jobs SET body=? WHERE id=?", d.JSON(original), original.ID)
	must(t, err)
	oldCandidate := snap.Candidate
	must(t, s.SaveProfile(ctx, u, d.Profile{Languages: []string{"Go", "Java"}, TargetRoles: []string{"后端开发"}}))
	if err := s.SaveCompanyReport(ctx, u, "", report, jobs); !errors.Is(err, p.ErrStaleInput) {
		t.Fatal("stale company result committed", err)
	}
	rec = matchingRequest(h, token, "/api/matching/company", "POST", map[string]any{"company": "Whole Synthetic", "scope": "SELECTED", "job_ids": ids})
	comparison = matching.CompanyComparison{}
	must(t, json.Unmarshal(rec.Body.Bytes(), &comparison))
	if rec.Code != 200 || comparison.Holistic != nil || oldCandidate.Hash() == comparison.HolisticInputKey {
		t.Fatal("stale company ranking shown", rec.Body.String())
	}
}
func TestWholeChatRoundTripPreservesCompanyRankingAndPartialImports(t *testing.T) {
	ctx, s, u, token, ids, _, h, m := wholeSetup(t)
	snap, err := s.MatchSnapshot(ctx, u, matching.ModelIdentity("server-default", "whole-fixture"), "张小明", ids)
	must(t, err)
	rec := matchingRequest(h, token, "/api/matching/export", "POST", map[string]any{"job_ids": ids, "candidate_hash": snap.CandidateHash, "mask_name": "张小明"})
	var exported struct {
		Version       string
		CandidateHash string `json:"candidate_hash"`
		Candidate     matching.Candidate
		Jobs          []matching.HolisticJob
		CompanyInputs []matching.HolisticCompanyInput `json:"company_inputs"`
	}
	must(t, json.Unmarshal(rec.Body.Bytes(), &exported))
	if rec.Code != 200 || exported.Version != matching.HolisticChatVersion {
		t.Fatal(rec.Code, rec.Body.String())
	}
	doc := matching.ChatDocument{Version: exported.Version, PromptRevision: matching.HolisticPromptRevision, CandidateHash: exported.CandidateHash, Jobs: []matching.ChatJob{}}
	var evidence []matching.Citation
	for _, f := range fixtureDocumentFacts(exported.Candidate) {
		if strings.HasSuffix(f.ID, "-description") {
			evidence = []matching.Citation{{ID: f.ID, Excerpt: f.Text}}
			break
		}
	}
	choices := []matching.CompanyChoice{}
	for i, j := range exported.Jobs {
		assessment := wholeAssessment(j.Text, evidence)
		doc.Jobs = append(doc.Jobs, matching.ChatJob{ID: j.ID, InputKey: j.InputKey, Assessment: &assessment})
		choices = append(choices, matching.CompanyChoice{ID: j.ID, Rank: i + 1, Reason: "核心工作相关", Advantage: "工程经验可以迁移", Tradeoff: "领域知识需要准备", JobExcerpt: j.Text, Evidence: evidence})
	}
	doc.Comparisons = []matching.HolisticCompanyReport{{Version: matching.HolisticVersion, Company: "Whole Synthetic", InputKey: exported.CompanyInputs[0].InputKey, CandidateHash: doc.CandidateHash, Summary: "先考虑工程经验更贴合的岗位", Choices: choices, Questions: []string{}}}
	rec = matchingRequest(h, token, "/api/matching/import/preview", "POST", map[string]any{"document": doc, "mask_name": "张小明"})
	var preview struct {
		Key         string `json:"preview_key"`
		Jobs        []json.RawMessage
		Comparisons []matching.HolisticCompanyReport
		Issues      []json.RawMessage
	}
	must(t, json.Unmarshal(rec.Body.Bytes(), &preview))
	if rec.Code != 200 || len(preview.Jobs) != 2 || len(preview.Comparisons) != 1 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = matchingRequest(h, token, "/api/matching/import/confirm", "POST", map[string]any{"document": doc, "preview_key": preview.Key, "mask_name": "张小明"})
	if rec.Code != 200 || m.calls.Load() != 0 {
		t.Fatal("chat round trip called API provider", rec.Code, rec.Body.String(), m.calls.Load())
	}
	rec = matchingRequest(h, token, "/api/matching/company", "POST", map[string]any{"company": "Whole Synthetic", "scope": "SELECTED", "job_ids": ids})
	var report matching.CompanyComparison
	must(t, json.Unmarshal(rec.Body.Bytes(), &report))
	if rec.Code != 200 || report.Holistic == nil || report.Holistic.Model != matching.ChatIdentity {
		t.Fatal("manual comparison lost", rec.Code, rec.Body.String())
	}
	old, err := s.MatchResult(ctx, u, ids[0])
	must(t, err)
	doc.Comparisons = nil
	doc.Jobs[0].Assessment.Strengths[0].Evidence = []matching.Citation{{ID: evidence[0].ID, Excerpt: "invented scale"}}
	rec = matchingRequest(h, token, "/api/matching/import/preview", "POST", map[string]any{"document": doc, "mask_name": "张小明"})
	must(t, json.Unmarshal(rec.Body.Bytes(), &preview))
	if rec.Code != 200 || len(preview.Jobs) != 1 || len(preview.Issues) != 1 {
		t.Fatal("invalid job prevented valid subset review", rec.Code, rec.Body.String())
	}
	rec = matchingRequest(h, token, "/api/matching/import/confirm", "POST", map[string]any{"document": doc, "preview_key": preview.Key, "mask_name": "张小明"})
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	unchanged, err := s.MatchResult(ctx, u, ids[0])
	must(t, err)
	if d.JSON(old) != d.JSON(unchanged) {
		t.Fatal("failed import overwrote valid report")
	}
}

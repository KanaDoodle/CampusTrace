package integration

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matcheval"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func TestCompanyWorkspaceRetainsWholeCampaignUsageAcrossSubsetAndAccounts(t *testing.T) {
	ctx, s, u, token, ids, api, h, m := wholeSetup(t)
	job, e := s.JobForUser(ctx, u, ids[0])
	must(t, e)
	_, e = s.SaveCampaign(ctx, u, "", p.ApplicationCampaign{CompanyID: job.CompanyID, Name: "2027共用名额", Limit: 1, JobIDs: ids, Confirmed: true})
	must(t, e)
	_, e = s.ApplyAction(ctx, u, d.ID(), "create_application", []byte(d.JSON(p.CreateArgs{JobID: ids[1], Resume: "private-resume-label"})))
	must(t, e)
	view := matchingRequest(h, token, "/api/matching/company-workspace", "POST", map[string]any{"company": "Whole Synthetic", "job_ids": ids[:1]})
	if view.Code != 200 || view.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(view.Code, view.Body.String())
	}
	var out struct {
		Comparison matching.CompanyComparison `json:"comparison"`
		Workflow   p.CompanyWorkflow          `json:"workflow"`
	}
	must(t, json.Unmarshal(view.Body.Bytes(), &out))
	if out.Comparison.Total != 1 || len(out.Workflow.Campaigns) != 1 || out.Workflow.Campaigns[0].Remaining != 0 || out.Workflow.Campaigns[0].Planned != 1 || len(out.Workflow.Applications) != 1 || out.Workflow.Applications[0].JobID != ids[1] || out.Workflow.Applications[0].Job.Title == "" || out.Workflow.Jobs[0].Application != nil {
		t.Fatal(out)
	}
	if strings.Contains(view.Body.String(), "private-resume-label") || m.calls.Load() != 0 {
		t.Fatal("view leaked unrelated application details or called a model")
	}
	other, e := s.NewUser(ctx, d.ID()+"@synthetic.test", "test-hash")
	must(t, e)
	t.Cleanup(func() { cleanupFixture(ctx, s, other, "") })
	must(t, s.SaveProfile(ctx, other, d.Profile{Languages: []string{"Go"}}))
	otherToken, e := api.Auth.Token(other)
	must(t, e)
	stolen := matchingRequest(h, otherToken, "/api/matching/company-workspace", "POST", map[string]any{"company": "Whole Synthetic", "job_ids": ids[:1]})
	if stolen.Code != 404 {
		t.Fatal("private decision visible to another account", stolen.Code, stolen.Body.String())
	}
	catalog := matchingRequest(h, otherToken, "/api/matching/company-catalog?company=Whole%20Synthetic", "GET", nil)
	if catalog.Code != 200 || strings.Contains(catalog.Body.String(), ids[0]) {
		t.Fatal(catalog.Code, catalog.Body.String())
	}
}

func TestMatchingEvaluationExportBindsHumanLabelToCurrentSanitizedScope(t *testing.T) {
	ctx, s, u, token, ids, _, h, m := wholeSetup(t)
	identity := matching.ModelIdentity("server-default", "whole-fixture")
	snap, e := s.MatchSnapshot(ctx, u, identity, "张小明", ids)
	must(t, e)
	jobs := p.WholeJobs(snap.Jobs, "张小明")
	report, e := matching.CompareHolistically(ctx, m, snap.Candidate, jobs, identity)
	must(t, e)
	report.Company = "Whole Synthetic"
	must(t, s.SaveCompanyReport(ctx, u, "张小明", report, jobs))
	calls := m.calls.Load()
	body := map[string]any{"company": "Whole Synthetic", "job_ids": ids, "mask_name": "张小明", "expected_scope_key": report.InputKey, "reference": matcheval.Reference{AcceptableTopIDs: []string{report.Choices[1].ID}, Reason: "人工更认可第二份的业务方向", Reviewed: true}}
	rec := matchingRequest(h, token, "/api/matching/evaluation/export", "POST", body)
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" || strings.Contains(rec.Body.String(), "private-whole@example.com") || strings.Contains(rec.Body.String(), "张小明") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var dataset matcheval.Dataset
	must(t, json.Unmarshal(rec.Body.Bytes(), &dataset))
	must(t, dataset.Validate())
	if len(dataset.Recorded) != 1 || len(dataset.Cases) != 1 || dataset.Cases[0].Source != "USER_REVIEWED_SNAPSHOT" || m.calls.Load() != calls {
		t.Fatal("export did not reuse current report", len(dataset.Recorded), m.calls.Load())
	}
	score := matcheval.Grade(dataset.Cases[0], dataset.Recorded[0])
	if !score.ContractPass || score.TopAgreement == nil || *score.TopAgreement {
		t.Fatal("manual preference overwritten by model", score)
	}
	body["reference"] = matcheval.Reference{AcceptableTopIDs: []string{d.ID()}, Reason: "越界岗位", Reviewed: true}
	rec = matchingRequest(h, token, "/api/matching/evaluation/export", "POST", body)
	if rec.Code != 400 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	body["reference"] = matcheval.Reference{AcceptableTopIDs: ids[:1], Reason: "正确范围", Reviewed: true}
	project, e := p.One[d.Project](ctx, s.DB, "SELECT body FROM projects WHERE user_id=?", u)
	must(t, e)
	project.Description += " 新增真实经历"
	project.Description = strings.ReplaceAll(project.Description, "private-whole@example.com", "[已遮盖邮箱]")
	_, e = s.SaveProject(ctx, u, project)
	must(t, e)
	rec = matchingRequest(h, token, "/api/matching/evaluation/export", "POST", body)
	if rec.Code != 409 || m.calls.Load() != calls {
		t.Fatal("stale label exported or invoked provider", rec.Code, rec.Body.String())
	}
}

func TestCompanyDecisionCatalogRequiresAuthentication(t *testing.T) {
	_, _, _, _, _, _, h, _ := wholeSetup(t)
	r := matchingRequest(h, "", "/api/matching/company-catalog", "GET", nil)
	if r.Code != http.StatusUnauthorized {
		t.Fatal(r.Code)
	}
}

func TestCompanyCandidatePickerUsesCurrentSummariesAndKeepsPrivateMaterialsOut(t *testing.T) {
	ctx, s, u, token, ids, api, h, m := wholeSetup(t)
	identity := matching.ModelIdentity("server-default", "whole-fixture")
	snap, e := s.MatchSnapshot(ctx, u, identity, "", ids)
	must(t, e)
	analyzed := matchingRequest(h, token, "/api/matching/analyze", "POST", map[string]any{"job_ids": ids, "candidate_hash": snap.CandidateHash})
	if analyzed.Code != 200 {
		t.Fatal(analyzed.Code, analyzed.Body.String())
	}
	calls := m.calls.Load()
	body := map[string]any{"company": "Whole Synthetic"}
	view := matchingRequest(h, token, "/api/matching/company-candidates", "POST", body)
	if view.Code != 200 || view.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(view.Code, view.Body.String())
	}
	var rows []struct {
		Job   p.ApplicationJob `json:"job"`
		State string           `json:"state"`
		Fit   string           `json:"fit"`
		Score *float64         `json:"score"`
	}
	must(t, json.Unmarshal(view.Body.Bytes(), &rows))
	if len(rows) != 2 || rows[0].State != "ANALYZED" || rows[0].Fit != "RELATED" || rows[0].Score != nil || len(rows[0].Job.Locations) != 1 || rows[0].Job.Locations[0] != "上海" {
		t.Fatal(rows)
	}
	for _, secret := range []string{"private-whole@example.com", "candidate_document", "explanation", "张小明", "完整背景"} {
		if strings.Contains(view.Body.String(), secret) {
			t.Fatal("picker returned private materials or detailed evidence", secret)
		}
	}
	other, e := s.NewUser(ctx, d.ID()+"@synthetic.test", "test-hash")
	must(t, e)
	t.Cleanup(func() { cleanupFixture(ctx, s, other, "") })
	must(t, s.SaveProfile(ctx, other, d.Profile{Languages: []string{"Go"}}))
	otherToken, e := api.Auth.Token(other)
	must(t, e)
	hidden := matchingRequest(h, otherToken, "/api/matching/company-candidates", "POST", body)
	if hidden.Code != 200 || strings.TrimSpace(hidden.Body.String()) != "[]" {
		t.Fatal(hidden.Code, hidden.Body.String())
	}
	unauth := matchingRequest(h, "", "/api/matching/company-candidates", "POST", body)
	if unauth.Code != http.StatusUnauthorized {
		t.Fatal(unauth.Code)
	}
	body["job_ids"] = ids
	invalid := matchingRequest(h, token, "/api/matching/company-candidates", "POST", body)
	if invalid.Code != 400 {
		t.Fatal(invalid.Code)
	}
	delete(body, "job_ids")
	must(t, s.SaveProfile(ctx, u, d.Profile{Languages: []string{"Go", "Python"}, TargetRoles: []string{"后端开发"}, Degree: "MASTER", GraduationYear: 2027}))
	stale := matchingRequest(h, token, "/api/matching/company-candidates", "POST", body)
	rows = nil
	must(t, json.Unmarshal(stale.Body.Bytes(), &rows))
	if stale.Code != 200 || rows[0].State != "STALE" || rows[0].Fit != "" || rows[0].Score != nil || m.calls.Load() != calls {
		t.Fatal("picker treated stale analysis as current or called a model", stale.Code, rows)
	}
}

func TestRadarSummaryPreviewPreservesInventoryAndLoadsEvidenceOnDemand(t *testing.T) {
	_, _, _, token, ids, _, h, m := wholeSetup(t)
	full := matchingRequest(h, token, "/api/matching/preview", "POST", map[string]any{})
	lean := matchingRequest(h, token, "/api/matching/preview", "POST", map[string]any{"summary_only": true})
	var a, b p.MatchSnapshot
	must(t, json.Unmarshal(full.Body.Bytes(), &a))
	must(t, json.Unmarshal(lean.Body.Bytes(), &b))
	if full.Code != 200 || lean.Code != 200 || len(a.Jobs) != len(b.Jobs) || a.CandidateHash != b.CandidateHash || len(a.Jobs) != 2 {
		t.Fatal(full.Code, lean.Code)
	}
	for i := range a.Jobs {
		if a.Jobs[i].Job.ID != b.Jobs[i].Job.ID || a.Jobs[i].InputKey != b.Jobs[i].InputKey || a.Jobs[i].State != b.Jobs[i].State || a.Jobs[i].PreliminaryScore != b.Jobs[i].PreliminaryScore || a.Jobs[i].Local.Tier != b.Jobs[i].Local.Tier || a.Jobs[i].Local.Direction.Status != b.Jobs[i].Local.Direction.Status || len(b.Jobs[i].Local.Checks) != 0 {
			t.Fatal("lean preview changed inventory decisions")
		}
	}
	if lean.Body.Len() >= full.Body.Len() {
		t.Fatal("summary preview did not reduce response size")
	}
	detail := matchingRequest(h, token, "/api/matching/results/"+ids[0], "POST", map[string]any{})
	var result struct {
		Local *matching.LocalScreen `json:"local"`
	}
	must(t, json.Unmarshal(detail.Body.Bytes(), &result))
	if detail.Code != 200 || result.Local == nil || len(result.Local.Checks) == 0 || m.calls.Load() != 0 {
		t.Fatal("details lost local evidence or made model calls")
	}
	t.Logf("synthetic preview full_bytes=%d summary_bytes=%d", full.Body.Len(), lean.Body.Len())
}

func TestCompanyCandidatePickerCanNarrowMoreThanTwoHundredJobs(t *testing.T) {
	ctx, s, u, token, _, _, h, m := wholeSetup(t)
	for i := 0; i < 200; i++ {
		_, e := s.IngestForUser(ctx, u, p.Ingest{Company: "Whole Synthetic", Title: "后端开发", JobType: "FULL_TIME", ExternalID: d.ID(), Locations: []string{"上海"}, Text: "任职要求：熟悉Go。", FetchStatus: "SUCCESS"})
		must(t, e)
	}
	picker := matchingRequest(h, token, "/api/matching/company-candidates", "POST", map[string]any{"company": "Whole Synthetic"})
	var rows []struct {
		Job p.ApplicationJob `json:"job"`
	}
	must(t, json.Unmarshal(picker.Body.Bytes(), &rows))
	if picker.Code != 200 || len(rows) != 202 || m.calls.Load() != 0 {
		t.Fatal(picker.Code, len(rows))
	}
	workspace := matchingRequest(h, token, "/api/matching/company-workspace", "POST", map[string]any{"company": "Whole Synthetic", "job_ids": []string{rows[0].Job.ID}})
	if workspace.Code != 200 || m.calls.Load() != 0 {
		t.Fatal(workspace.Code, workspace.Body.String())
	}
}

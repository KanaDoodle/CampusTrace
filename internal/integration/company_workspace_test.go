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

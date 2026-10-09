package integration

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func TestCompanyChatCommitFencesConcurrentReportAndJDChanges(t *testing.T) {
	ctx, s, u, token, ids, _, h, m := wholeSetup(t)
	rec := matchingRequest(h, token, "/api/matching/company-chat/export", "POST", map[string]any{"company": "Whole Synthetic", "job_ids": ids})
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var pack companyChatPack
	must(t, json.Unmarshal(rec.Body.Bytes(), &pack))
	doc := fillCompanyChat(t, pack)
	snapshot, err := s.MatchImportSnapshot(ctx, u, "", ids)
	must(t, err)
	jobs := p.WholeJobs(snapshot.Jobs)
	report, err := matching.PrepareCompanyChat(doc, snapshot.Candidate, jobs)
	must(t, err)
	previous, err := s.CompanyChatVersion(ctx, u, report.Company, report.InputKey)
	must(t, err)
	must(t, s.SaveReviewedCompanyReport(ctx, u, "", report, jobs, previous))
	changed := report
	changed.Summary = "未重新预览的并发覆盖"
	if err := s.SaveReviewedCompanyReport(ctx, u, "", changed, jobs, previous); !errors.Is(err, p.ErrStaleInput) {
		t.Fatal("commit ignored concurrent comparison", err)
	}
	previous, err = s.CompanyChatVersion(ctx, u, report.Company, report.InputKey)
	must(t, err)
	o, err := p.One[d.Observation](ctx, s.DB, "SELECT body FROM observations WHERE job_id=? ORDER BY observed_at DESC,id DESC LIMIT 1", ids[0])
	must(t, err)
	o.Text += " 新增业务要求。"
	_, err = s.DB.ExecContext(ctx, "UPDATE observations SET body=? WHERE id=?", d.JSON(o), o.ID)
	must(t, err)
	if err := s.SaveReviewedCompanyReport(ctx, u, "", changed, jobs, previous); !errors.Is(err, p.ErrStaleInput) {
		t.Fatal("commit ignored changed JD", err)
	}
	saved, err := s.CompanyReport(ctx, u, report.Company, report.InputKey)
	must(t, err)
	if saved.Summary != report.Summary || m.calls.Load() != 0 {
		t.Fatal("rejected commit changed report or called provider")
	}
}

type companyChatPack struct {
	Document  matching.CompanyChatDocument `json:"document"`
	Candidate matching.Candidate           `json:"candidate"`
	Jobs      []matching.HolisticJob       `json:"jobs"`
	Prompt    string                       `json:"prompt"`
}

func fillCompanyChat(t *testing.T, pack companyChatPack) matching.CompanyChatDocument {
	t.Helper()
	doc := pack.Document
	var evidence matching.Fact
	for _, f := range fixtureDocumentFacts(pack.Candidate) {
		if strings.HasSuffix(f.ID, "-description") {
			evidence = f
			break
		}
	}
	if evidence.ID == "" {
		t.Fatal("no complete project in export")
	}
	doc.Summary = "GPT 直接比较完整材料，优先考虑基础平台"
	doc.Choices = nil
	for i, job := range pack.Jobs {
		doc.Choices = append(doc.Choices, matching.CompanyChoice{ID: job.ID, Rank: len(pack.Jobs) - i,
			Reason: "服务能力可迁移，业务领域待确认", Advantage: "有事务与重试实践", Tradeoff: "当前资料未体现生产值守",
			JobExcerpt: strings.ReplaceAll(job.Text, "\n", ""), Evidence: []matching.Citation{{ID: evidence.ID, Excerpt: strings.ReplaceAll(evidence.Text, "\n", "")}}})
	}
	return doc
}

func TestCompanyChatRoundTripNeedsNoSingleJobAnalysisAndShowsNewestReport(t *testing.T) {
	ctx, s, u, token, ids, _, h, m := wholeSetup(t)
	// An older API report must not hide a newly accepted manual comparison.
	identity := matching.ModelIdentity("server-default", "whole-fixture")
	snap, err := s.MatchSnapshot(ctx, u, identity, "", ids)
	must(t, err)
	older, err := matching.CompareHolistically(ctx, m, snap.Candidate, p.WholeJobs(snap.Jobs), identity)
	must(t, err)
	must(t, s.SaveCompanyReport(ctx, u, "", older, p.WholeJobs(snap.Jobs)))
	calls := m.calls.Load()
	before, err := s.MatchResultVersions(ctx, u, ids)
	must(t, err)
	export := matchingRequest(h, token, "/api/matching/company-chat/export", "POST", map[string]any{"company": "Whole Synthetic", "job_ids": ids, "mask_name": "张小明"})
	if export.Code != 200 || export.Header().Get("Cache-Control") != "no-store" || strings.Contains(export.Body.String(), "private-whole@example.com") || strings.Contains(export.Body.String(), "张小明") || strings.Contains(export.Body.String(), older.Summary) {
		t.Fatal(export.Code, export.Body.String())
	}
	var pack companyChatPack
	must(t, json.Unmarshal(export.Body.Bytes(), &pack))
	if pack.Prompt != matching.CompanyChatPrompt || len(pack.Jobs) != 2 || !strings.Contains(pack.Candidate.Document, "完整背景") {
		t.Fatal("export lost whole context", pack)
	}
	doc := fillCompanyChat(t, pack)
	body := map[string]any{"document": doc, "mask_name": "张小明"}
	preview := matchingRequest(h, token, "/api/matching/company-chat/preview", "POST", body)
	if preview.Code != 200 {
		t.Fatal(preview.Code, preview.Body.String())
	}
	var out struct {
		Key    string                         `json:"preview_key"`
		Report matching.HolisticCompanyReport `json:"report"`
	}
	must(t, json.Unmarshal(preview.Body.Bytes(), &out))
	var count int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM company_match_reports WHERE user_id=?", u).Scan(&count))
	if count != 1 || !strings.Contains(out.Report.Choices[0].JobExcerpt, "服\n务") {
		t.Fatal("preview wrote data or did not restore exact quote", count, out.Report)
	}
	body["preview_key"] = out.Key
	changed := doc
	changed.Summary += " 改动未预览"
	body["document"] = changed
	if r := matchingRequest(h, token, "/api/matching/company-chat/confirm", "POST", body); r.Code != 409 {
		t.Fatal("edited result bypassed preview", r.Code, r.Body.String())
	}
	body["document"] = doc
	confirm := matchingRequest(h, token, "/api/matching/company-chat/confirm", "POST", body)
	if confirm.Code != 200 {
		t.Fatal(confirm.Code, confirm.Body.String())
	}
	if repeat := matchingRequest(h, token, "/api/matching/company-chat/confirm", "POST", body); repeat.Code != 409 {
		t.Fatal("old preview replay overwrote a saved report", repeat.Code)
	}
	view := matchingRequest(h, token, "/api/matching/company-workspace", "POST", map[string]any{"company": "Whole Synthetic", "job_ids": ids})
	var workspace struct {
		Comparison matching.CompanyComparison `json:"comparison"`
	}
	must(t, json.Unmarshal(view.Body.Bytes(), &workspace))
	if view.Code != 200 || workspace.Comparison.Holistic == nil || workspace.Comparison.Holistic.Summary != doc.Summary || workspace.Comparison.Holistic.Model != matching.ChatIdentity || workspace.Comparison.Holistic.Choices[0].Rank != doc.Choices[0].Rank {
		t.Fatal("import hidden on normal unmasked reload", view.Code, view.Body.String())
	}
	after, err := s.MatchResultVersions(ctx, u, ids)
	must(t, err)
	if d.JSON(before) != d.JSON(after) || m.calls.Load() != calls {
		t.Fatal("company import changed single jobs or called provider")
	}
}

func TestCompanyChatRejectsChangedSourcesForeignAccountsAndMalformedScope(t *testing.T) {
	ctx, s, u, token, ids, api, h, m := wholeSetup(t)
	request := map[string]any{"company": "Whole Synthetic", "job_ids": ids}
	rec := matchingRequest(h, token, "/api/matching/company-chat/export", "POST", request)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var pack companyChatPack
	must(t, json.Unmarshal(rec.Body.Bytes(), &pack))
	doc := fillCompanyChat(t, pack)
	body := map[string]any{"document": doc}
	preview := matchingRequest(h, token, "/api/matching/company-chat/preview", "POST", body)
	var out struct {
		Key string `json:"preview_key"`
	}
	must(t, json.Unmarshal(preview.Body.Bytes(), &out))
	if preview.Code != 200 {
		t.Fatal(preview.Code, preview.Body.String())
	}
	other, err := s.NewUser(ctx, d.ID()+"@synthetic.test", "test-hash")
	must(t, err)
	t.Cleanup(func() { cleanupFixture(ctx, s, other, "") })
	must(t, s.SaveProfile(ctx, other, d.Profile{Languages: []string{"Go"}}))
	otherToken, err := api.Auth.Token(other)
	must(t, err)
	for _, route := range []string{"export", "preview"} {
		payload := body
		if route == "export" {
			payload = request
		}
		r := matchingRequest(h, otherToken, "/api/matching/company-chat/"+route, "POST", payload)
		if r.Code != 404 {
			t.Fatal("foreign source visible", route, r.Code, r.Body.String())
		}
	}
	bad := doc
	bad.Choices = bad.Choices[:1]
	body["document"] = bad
	if r := matchingRequest(h, token, "/api/matching/company-chat/preview", "POST", body); r.Code != 400 {
		t.Fatal("partial comparison accepted", r.Code)
	}
	bad = doc
	bad.Choices = append([]matching.CompanyChoice{}, doc.Choices...)
	bad.Choices[0].JobExcerpt = "编造的原文"
	body["document"] = bad
	if r := matchingRequest(h, token, "/api/matching/company-chat/preview", "POST", body); r.Code != 400 || !strings.Contains(r.Body.String(), "COMPANY_CHOICE") {
		t.Fatal("fabricated quote accepted", r.Code, r.Body.String())
	}
	body["document"] = doc
	body["preview_key"] = out.Key
	must(t, s.SaveProfile(ctx, u, d.Profile{Languages: []string{"Go", "Java"}, GraduationYear: 2027, Degree: "MASTER"}))
	if r := matchingRequest(h, token, "/api/matching/company-chat/confirm", "POST", body); r.Code != 409 {
		t.Fatal("stale profile accepted", r.Code, r.Body.String())
	}
	var count int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM company_match_reports WHERE user_id=?", u).Scan(&count))
	if count != 0 || m.calls.Load() != 0 {
		t.Fatal("rejection wrote or called provider", count, m.calls.Load())
	}
	if r := matchingRequest(h, "", "/api/matching/company-chat/export", "POST", request); r.Code != 401 {
		t.Fatal(r.Code)
	}
}

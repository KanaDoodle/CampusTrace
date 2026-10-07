package integration

import (
	"encoding/json"
	"errors"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChatImportPreviewConfirmStalenessOwnershipAndDecisionFlows(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	profile := d.Profile{Degree: "MASTER", GraduationYear: 2027, Languages: []string{"Go"}}
	must(t, s.SaveProfile(ctx, u, profile))
	authn := auth.Service{Store: s, Secret: []byte("synthetic-chat-import-secret-32-bytes")}
	token, err := authn.Token(u)
	must(t, err)
	model := &matchFixtureModel{}
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New(), ResumeModel: model, ResumeModelName: "fixture"}).Handler()
	ids := []string{}
	inputs := []p.Ingest{}
	for i := 0; i < 2; i++ {
		in := p.Ingest{Company: "Chat fixture", Title: "后端开发", JobType: "FULL_TIME", ExternalID: d.ID(), Text: "熟悉 Go", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()}
		o, e := s.IngestForUser(ctx, u, in)
		must(t, e)
		ids = append(ids, o.JobID)
		inputs = append(inputs, in)
	}
	snapshot, err := s.MatchImportSnapshot(ctx, u, "", ids)
	must(t, err)
	rec := matchingRequest(handler, token, "/api/matching/export", "POST", map[string]any{"job_ids": ids, "candidate_hash": snapshot.CandidateHash})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), matching.ChatVersion) || !strings.Contains(rec.Body.String(), snapshot.Jobs[0].InputKey) {
		t.Fatal(rec.Code, rec.Body.String())
	}
	doc := matching.ChatDocument{Version: matching.ChatVersion, CandidateHash: snapshot.CandidateHash, Jobs: []matching.ChatJob{}}
	for _, row := range snapshot.Jobs {
		doc.Jobs = append(doc.Jobs, matching.ChatJob{ID: row.Job.ID, InputKey: row.InputKey, Requirements: []matching.Requirement{{ID: "go", Category: "REQUIRED", Text: "熟悉 Go", Excerpt: "熟悉 Go", Confidence: 1}}, Matches: []matching.Match{{RequirementID: "go", Result: "DIRECT", Explanation: "已保存 Go", Evidence: []matching.Citation{{ID: "language-0", Excerpt: "Go"}}}}})
	}
	request := func(action string, body any) *httptest.ResponseRecorder {
		return matchingRequest(handler, token, "/api/matching/import/"+action, "POST", body)
	}
	body := map[string]any{"document": doc, "mask_name": ""}
	rec = request("preview", body)
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var preview struct {
		Key  string `json:"preview_key"`
		Jobs []struct {
			Result   matching.Result `json:"result"`
			Replaces bool            `json:"replaces"`
		} `json:"jobs"`
	}
	must(t, json.Unmarshal(rec.Body.Bytes(), &preview))
	if len(preview.Jobs) != 2 || *preview.Jobs[0].Result.Score != 100 || preview.Jobs[0].Replaces {
		t.Fatal(preview)
	}
	if _, err = s.MatchResult(ctx, u, ids[0]); !errors.Is(err, p.ErrNotFound) {
		t.Fatal("preview wrote result", err)
	}
	if rec = request("confirm", body); rec.Code != 409 {
		t.Fatal("save bypassed preview", rec.Code)
	}
	body["preview_key"] = preview.Key
	must(t, q.R.Set(ctx, q.Prefix+"matching:lease:"+u, "synthetic-held-import-lease", time.Minute).Err())
	if rec = request("confirm", body); rec.Code != 409 || !strings.Contains(rec.Body.String(), "MATCH_BUSY") {
		t.Fatal("import raced active API analysis", rec.Code, rec.Body.String())
	}
	must(t, q.R.Del(ctx, q.Prefix+"matching:lease:"+u).Err())
	if rec = request("confirm", body); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec = request("confirm", body); rec.Code != 409 {
		t.Fatal("double confirm reused old preview", rec.Code)
	}
	after, err := s.MatchSnapshot(ctx, u, matching.ModelIdentity("https://other.example", "another-model"), "", ids)
	must(t, err)
	if after.CallsToday != 0 || model.calls.Load() != 0 || after.Jobs[0].State != "ANALYZED" || after.Jobs[0].Source != matching.ChatSource {
		t.Fatal(after.Jobs, after.CallsToday)
	}
	rec = matchingRequest(handler, token, "/api/matching/company", "POST", map[string]any{"company": "Chat fixture"})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"analyzed":2`) {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = matchingRequest(handler, token, "/api/matching/preparation/"+ids[0], "POST", map[string]any{})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "熟悉 Go") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	// Previewed replacement is fenced by the existing result, not just the input.
	rec = request("preview", body)
	must(t, json.Unmarshal(rec.Body.Bytes(), &preview))
	body["preview_key"] = preview.Key
	old, err := s.MatchResult(ctx, u, ids[0])
	must(t, err)
	old.AnalyzedAt = old.AnalyzedAt.Add(time.Second)
	must(t, s.SaveMatchResult(ctx, u, "", old))
	if rec = request("confirm", body); rec.Code != 409 {
		t.Fatal("overwrote concurrent result", rec.Code)
	}
	other, err := s.NewUser(ctx, d.ID()+"@chat-other.invalid", "unused")
	must(t, err)
	must(t, s.SaveProfile(ctx, other, profile))
	otherToken, err := authn.Token(other)
	must(t, err)
	if rec = matchingRequest(handler, otherToken, "/api/matching/import/preview", "POST", body); rec.Code != 404 {
		t.Fatal("cross-owner import", rec.Code)
	}
	if rec = matchingRequest(handler, "", "/api/matching/import/preview", "POST", body); rec.Code != 401 {
		t.Fatal("unauthenticated import", rec.Code)
	}
	invalid := doc
	invalid.Jobs = append([]matching.ChatJob{}, doc.Jobs...)
	invalid.Jobs[1].Matches = []matching.Match{{RequirementID: "go", Result: "DIRECT", Explanation: "伪造", Evidence: []matching.Citation{{ID: "language-0", Excerpt: "Java"}}}}
	if rec = request("preview", map[string]any{"document": invalid, "mask_name": ""}); rec.Code != 400 || !strings.Contains(rec.Body.String(), "EXCERPT_NOT_EXACT") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	// All-or-nothing save even if a later job has changed since preview.
	rec = request("preview", body)
	must(t, json.Unmarshal(rec.Body.Bytes(), &preview))
	versions, err := s.MatchResultVersions(ctx, u, ids)
	must(t, err)
	results := []matching.Result{}
	for _, row := range preview.Jobs {
		results = append(results, row.Result)
	}
	changedIndex := 1
	if ids[0] > ids[1] {
		changedIndex = 0
	}
	inputs[changedIndex].Text = "熟悉 Java"
	inputs[changedIndex].ObservedAt = time.Now().Add(time.Second).UTC()
	_, err = s.IngestForUser(ctx, u, inputs[changedIndex])
	must(t, err)
	if err = s.SaveChatMatches(ctx, u, "", results, versions); !errors.Is(err, p.ErrStaleInput) {
		t.Fatal(err)
	}
	check, err := s.MatchResult(ctx, u, ids[0])
	must(t, err)
	if d.JSON(check) != d.JSON(old) {
		t.Fatal("failed batch partly overwrote results")
	}
	profile.Skills = []string{"Redis"}
	must(t, s.SaveProfile(ctx, u, profile))
	if rec = request("preview", body); rec.Code != 409 {
		t.Fatal("stale profile imported", rec.Code)
	}
}
func TestMaskedChatImportRemainsUsableAfterReloadWithoutStoringMask(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{Languages: []string{"Go"}}))
	project, err := s.SaveProject(ctx, u, d.Project{Name: "队列"})
	must(t, err)
	_, err = s.SaveFact(ctx, u, d.ProjectFact{ProjectID: project.ID, Kind: "IMPLEMENTED", Claim: "张小明实现 Go 服务", Verified: true})
	must(t, err)
	o, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Masked fixture", Title: "后端", ExternalID: d.ID(), JobType: "FULL_TIME", Text: "熟悉 Go", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, err)
	authn := auth.Service{Store: s, Secret: []byte("synthetic-masked-chat-secret-32-bytes")}
	token, err := authn.Token(u)
	must(t, err)
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New()}).Handler()
	snapshot, err := s.MatchImportSnapshot(ctx, u, "张小明", []string{o.JobID})
	must(t, err)
	var f matching.Fact
	for _, fact := range snapshot.Candidate.Facts {
		if fact.Kind == "IMPLEMENTED" {
			f = fact
		}
	}
	doc := matching.ChatDocument{Version: matching.ChatVersion, CandidateHash: snapshot.CandidateHash, Jobs: []matching.ChatJob{{ID: o.JobID, InputKey: snapshot.Jobs[0].InputKey, Requirements: []matching.Requirement{{ID: "go", Category: "REQUIRED", Text: "熟悉 Go", Excerpt: "熟悉 Go", Confidence: 1}}, Matches: []matching.Match{{RequirementID: "go", Result: "DIRECT", Explanation: "有 Go 实现", Evidence: []matching.Citation{{ID: f.ID, Excerpt: f.Text}}}}}}}
	body := map[string]any{"document": doc, "mask_name": "张小明"}
	rec := matchingRequest(handler, token, "/api/matching/import/preview", "POST", body)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var preview struct {
		Key string `json:"preview_key"`
	}
	must(t, json.Unmarshal(rec.Body.Bytes(), &preview))
	body["preview_key"] = preview.Key
	rec = matchingRequest(handler, token, "/api/matching/import/confirm", "POST", body)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	after, err := s.MatchDecisionSnapshot(ctx, u, "other-model", "", []string{o.JobID}, "")
	must(t, err)
	if after.Jobs[0].State != "ANALYZED" || strings.Contains(d.JSON(after.Jobs[0].Result), "张小明") {
		t.Fatal("masked import lost identity on reload", after.Jobs[0])
	}
}

func TestChatImportSharedQualificationExcerptPreviewAndConfirm(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	profile := d.Profile{Degree: "MASTER", Majors: []string{"软件工程"}, Languages: []string{"Go"}}
	must(t, s.SaveProfile(ctx, u, profile))
	quote := "本科及以上学历，计算机或软件工程相关专业"
	o, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Shared qualification fixture", Title: "后端开发", JobType: "FULL_TIME", ExternalID: d.ID(), Text: quote + "。熟悉 Go", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, err)
	snapshot, err := s.MatchImportSnapshot(ctx, u, "", []string{o.JobID})
	must(t, err)
	row := snapshot.Jobs[0]
	doc := matching.ChatDocument{Version: matching.ChatVersion, CandidateHash: snapshot.CandidateHash, Jobs: []matching.ChatJob{{ID: o.JobID, InputKey: row.InputKey,
		Requirements: []matching.Requirement{
			{ID: "degree", Category: "QUALIFICATION", Aspect: "TECHNICAL", Text: quote, Excerpt: quote, ClaimType: "EDUCATION_REQUIREMENT", Value: "BACHELOR", Confidence: 1},
			{ID: "major", Category: "QUALIFICATION", Aspect: "TECHNICAL", Text: quote, Excerpt: quote, ClaimType: "MAJOR_REQUIREMENT", Value: "计算机|软件工程", Confidence: 1},
			{ID: "go", Category: "REQUIRED", Text: "熟悉 Go", Excerpt: "熟悉 Go", Confidence: 1},
		}, Matches: []matching.Match{
			{RequirementID: "degree", Result: "NO_EVIDENCE", Explanation: "由本地核对", Evidence: []matching.Citation{}},
			{RequirementID: "major", Result: "NO_EVIDENCE", Explanation: "由本地核对", Evidence: []matching.Citation{}},
			{RequirementID: "go", Result: "DIRECT", Explanation: "已记录 Go", Evidence: []matching.Citation{{ID: "language-0", Excerpt: "Go"}}},
		},
	}}}
	authn := auth.Service{Store: s, Secret: []byte("synthetic-shared-qualification-32-bytes")}
	token, err := authn.Token(u)
	must(t, err)
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New()}).Handler()
	body := map[string]any{"document": doc, "mask_name": ""}
	rec := matchingRequest(handler, token, "/api/matching/import/preview", "POST", body)
	if rec.Code != 200 {
		t.Fatal("shared quote falsely rejected as duplicate", rec.Code, rec.Body.String())
	}
	var preview struct {
		Key string `json:"preview_key"`
	}
	must(t, json.Unmarshal(rec.Body.Bytes(), &preview))
	body["preview_key"] = preview.Key
	rec = matchingRequest(handler, token, "/api/matching/import/confirm", "POST", body)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	result, err := s.MatchResult(ctx, u, o.JobID)
	must(t, err)
	if len(result.Requirements) != 3 || len(result.Matches) != 3 || result.Score == nil || *result.Score != 100 {
		t.Fatal("lost facets or inflated core units", result)
	}
	checked := 0
	for _, rule := range result.Qualifications.Results {
		if rule.Rule == "EDUCATION_REQUIREMENT" || rule.Rule == "MAJOR_REQUIREMENT" {
			if rule.Result != "PASS" {
				t.Fatal("qualification did not remain independently checked", rule)
			}
			checked++
		}
	}
	if checked != 2 {
		t.Fatal("missing degree or major check", result.Qualifications.Results)
	}
	// A different ID cannot disguise two copies of the same qualification.
	bad := doc
	bad.Jobs = append([]matching.ChatJob{}, doc.Jobs...)
	bad.Jobs[0].Requirements = append([]matching.Requirement{}, doc.Jobs[0].Requirements...)
	bad.Jobs[0].Requirements[1].ClaimType = "EDUCATION_REQUIREMENT"
	bad.Jobs[0].Requirements[1].Value = "BACHELOR"
	rec = matchingRequest(handler, token, "/api/matching/import/preview", "POST", map[string]any{"document": bad, "mask_name": ""})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "CHAT_REQUIREMENT_CONTENT_DUPLICATE") || !strings.Contains(rec.Body.String(), `"related_item_index":1`) {
		t.Fatal("real duplicates were accepted or not located", rec.Code, rec.Body.String())
	}
	stored, err := s.MatchResult(ctx, u, o.JobID)
	must(t, err)
	if d.JSON(stored) != d.JSON(result) {
		t.Fatal("failed preview changed a saved result")
	}
}

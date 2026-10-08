package integration

import (
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

func TestChatSourceLabelRepairsPreviewConfirmAndReload(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{Degree: "MASTER", Languages: []string{"Go"}}))
	project, err := s.SaveProject(ctx, u, d.Project{Name: "Synthetic RPC"})
	must(t, err)
	_, err = s.SaveFact(ctx, u, d.ProjectFact{ProjectID: project.ID, Kind: "IMPLEMENTED", Claim: "实现 Go RPC 请求复用", Verified: true})
	must(t, err)
	duty := "负责直播系统架构设计，与产品、设计、测试团队紧密协作"
	language := "熟练掌握 Go、Java、C++ 中至少一种语言"
	source := "工作职责:\n" + duty + "\n工作要求:\n" + language
	o, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Source label fixture", Title: "后端开发", JobType: "FULL_TIME", ExternalID: d.ID(), Text: source, FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, err)
	snapshot, err := s.MatchImportSnapshot(ctx, u, "", []string{o.JobID})
	must(t, err)
	factID := ""
	for _, f := range snapshot.Candidate.Facts {
		if f.Kind == "IMPLEMENTED" && f.Text == "实现 Go RPC 请求复用" {
			factID = f.ID
		}
	}
	if factID == "" {
		t.Fatal("fixture fact missing")
	}
	doc := matching.ChatDocument{Version: matching.ChatVersion, CandidateHash: snapshot.CandidateHash, Jobs: []matching.ChatJob{{ID: o.JobID, InputKey: snapshot.Jobs[0].InputKey,
		Requirements: []matching.Requirement{
			{ID: "soft", Category: "RESPONSIBILITY", Aspect: "SOFT", Text: "协作", Excerpt: duty, Confidence: 1},
			{ID: "duty", Category: "RESPONSIBILITY", Aspect: "TECHNICAL", Text: duty, Excerpt: duty, Confidence: 1},
			{ID: "heading", Category: "RESPONSIBILITY", Text: "工作要求:", Excerpt: duty + "\n工作要求:", Confidence: 1},
			{ID: "generic", Category: "RESPONSIBILITY", Text: "掌握至少一种/一门岗位列举的主流编程语言", Excerpt: language, Confidence: 1},
			{ID: "language", Category: "RESPONSIBILITY", Text: language, Excerpt: language, Confidence: 1},
		}, Matches: []matching.Match{
			{RequirementID: "soft", Result: "NO_EVIDENCE", Explanation: "未记录团队协作", Evidence: []matching.Citation{}},
			{RequirementID: "duty", Result: "TRANSFERABLE", Explanation: "RPC 机制与服务研发相关，直播业务待了解", Evidence: []matching.Citation{{ID: factID, Excerpt: "Go RPC 请求复用"}}},
			{RequirementID: "heading", Result: "NO_EVIDENCE", Explanation: "章节标题", Evidence: []matching.Citation{}},
			{RequirementID: "generic", Result: "DIRECT", Explanation: "已填写语言", Evidence: []matching.Citation{{ID: "language-0", Excerpt: "Go"}}},
			{RequirementID: "language", Result: "PARTIAL", Explanation: "相关语言，熟练程度待核对", Evidence: []matching.Citation{{ID: "language-0", Excerpt: "Go"}}},
		},
	}}}
	authn := auth.Service{Store: s, Secret: []byte("synthetic-source-label-32-bytes-key")}
	token, err := authn.Token(u)
	must(t, err)
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New()}).Handler()
	body := map[string]any{"document": doc, "mask_name": ""}
	rec := matchingRequest(handler, token, "/api/matching/import/preview", "POST", body)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var preview struct {
		Key  string `json:"preview_key"`
		Jobs []struct {
			Result matching.Result `json:"result"`
		} `json:"jobs"`
	}
	must(t, json.Unmarshal(rec.Body.Bytes(), &preview))
	if len(preview.Jobs) != 1 || preview.Jobs[0].Result.IgnoredHeadings != 1 || preview.Jobs[0].Result.RestoredCategories != 2 || preview.Jobs[0].Result.Score == nil || *preview.Jobs[0].Result.Score != 50 {
		t.Fatal(preview)
	}
	body["preview_key"] = preview.Key
	rec = matchingRequest(handler, token, "/api/matching/import/confirm", "POST", body)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	result, err := s.MatchResult(ctx, u, o.JobID)
	must(t, err)
	if result.RestoredCategories != 2 || result.IgnoredHeadings != 1 || len(result.Requirements) != 3 || len(result.Matches) != 3 || result.Score == nil || *result.Score != 50 {
		t.Fatal(result)
	}
	for _, r := range result.Requirements {
		if r.ID == "duty" && strings.Contains(r.Text, "协作") {
			t.Fatal("duplicate soft clause remained", r)
		}
	}
}

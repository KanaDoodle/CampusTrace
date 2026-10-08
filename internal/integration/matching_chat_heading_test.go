package integration

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
)

func TestChatHeadingRecoveryPreviewConfirmAndReload(t *testing.T) {
	ctx, s, q, u, _ := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{Degree: "MASTER", Languages: []string{"Go"}}))
	o, err := s.IngestForUser(ctx, u, p.Ingest{Company: "Heading fixture", Title: "后端开发", JobType: "FULL_TIME", ExternalID: d.ID(), Text: "专业能力：\n专业不限，有技术洞察力\n熟悉 Go", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, err)
	snapshot, err := s.MatchImportSnapshot(ctx, u, "", []string{o.JobID})
	must(t, err)
	doc := matching.ChatDocument{Version: matching.ChatVersion, CandidateHash: snapshot.CandidateHash, Jobs: []matching.ChatJob{{ID: o.JobID, InputKey: snapshot.Jobs[0].InputKey,
		Requirements: []matching.Requirement{
			{ID: "header", Category: "QUALIFICATION", Text: "专业能力：", Excerpt: "专业能力：", ClaimType: "MAJOR_REQUIREMENT", Confidence: .95},
			{ID: "major", Category: "QUALIFICATION", Text: "专业不限", Excerpt: "专业不限，有技术洞察力", ClaimType: "MAJOR_REQUIREMENT", Confidence: .95},
			{ID: "go", Category: "REQUIRED", Text: "熟悉 Go", Excerpt: "熟悉 Go", Confidence: 1},
		}, Matches: []matching.Match{
			{RequirementID: "header", Result: "NO_EVIDENCE", Explanation: "分段标题", Evidence: []matching.Citation{}},
			{RequirementID: "major", Result: "NO_EVIDENCE", Explanation: "本地核对", Evidence: []matching.Citation{}},
			{RequirementID: "go", Result: "DIRECT", Explanation: "已记录 Go", Evidence: []matching.Citation{{ID: "language-0", Excerpt: "Go"}}},
		},
	}}}
	authn := auth.Service{Store: s, Secret: []byte("synthetic-heading-recovery-32-bytes")}
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
	if len(preview.Jobs) != 1 || preview.Jobs[0].Result.IgnoredHeadings != 1 || len(preview.Jobs[0].Result.Requirements) != 2 {
		t.Fatal(preview)
	}
	body["preview_key"] = preview.Key
	rec = matchingRequest(handler, token, "/api/matching/import/confirm", "POST", body)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	result, err := s.MatchResult(ctx, u, o.JobID)
	must(t, err)
	if result.IgnoredHeadings != 1 || len(result.Requirements) != 2 || len(result.Matches) != 2 || result.Score == nil || *result.Score != 100 {
		t.Fatal(result)
	}
	found := false
	for _, rule := range result.Qualifications.Results {
		if rule.Rule == "MAJOR_REQUIREMENT" {
			found = true
			if rule.Result != "NOT_APPLICABLE" {
				t.Fatal(rule)
			}
		}
	}
	if !found {
		t.Fatal("lost explicit unrestricted major")
	}
}

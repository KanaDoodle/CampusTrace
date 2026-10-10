package transport

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func TestInventoryPreviewPreservesBrowseFieldsWithoutPrivateEvidence(t *testing.T) {
	at := time.Now().UTC()
	score := 70.0
	v := p.MatchSnapshot{CandidateHash: "full-reviewed-input", CandidateDocument: "PRIVATE full resume", Candidate: matching.Candidate{Document: "PRIVATE full resume", Facts: []matching.Fact{{Kind: "ROLE", Text: "后端开发"}, {Kind: "CITY_PREFERRED", Text: "上海"}, {Kind: "CITY_ACCEPTABLE", Text: "北京"}, {Kind: "SKILL", Text: "PRIVATE skills"}}, Projects: []matching.CandidateProject{{Description: "PRIVATE project"}}}, Jobs: []p.MatchJob{{Job: d.Job{ID: "job", Company: "合成公司", CompanyID: "company", Title: "服务端开发", JobType: "FULL_TIME", Locations: []string{"上海市"}, CurrentStatus: "OPEN", CreatedAt: at, UpdatedAt: at, OwnerID: "PRIVATE owner", Fingerprint: "PRIVATE raw metadata"}, Cities: []string{"上海"}, RequirementsKey: "jd-key", InputKey: "input-key", TextBytes: 300, PreliminaryScore: 60, Score: &score, State: "ANALYZED", Disposition: "SAVED", AnalysisMode: "HOLISTIC", Source: "CHATGPT_IMPORT", Coverage: 80, Priority: &matching.Priority{Score: 75, Lower: 70, Upper: 80}, CompanyPlacement: &matching.CompanyPlacement{Rank: 1, Total: 2}, Application: &p.MatchApplication{ID: "application", JobID: "job", State: "INTERVIEW", Version: 2}, Local: &matching.LocalScreen{Version: matching.LocalVersion, Score: 60, Tier: "POSSIBLE", Role: "backend", Reasons: []string{"第一条说明", "PRIVATE detailed reason"}, Warnings: []string{"PRIVATE warning"}, Checks: []matching.LocalCheck{{Excerpt: "PRIVATE source"}}, Direction: matching.Direction{Status: "MATCH", Reason: "PRIVATE fallback reason"}}, Holistic: &matching.HolisticAssessment{Fit: "RELATED", Summary: "PRIVATE model detail"}}}}
	v.Jobs[0].Campaign = &p.MatchCampaign{ID: "quota", Name: "同批次限投", Limit: 2, Submitted: 2, HideUnsubmitted: true}
	before, _ := json.Marshal(v)
	out := inventoryPreview(v)
	body, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "PRIVATE") || len(body) >= len(before) || len(out.Candidate.Facts) != 3 || out.CandidateHash != v.CandidateHash {
		t.Fatal("summary leaked materials or changed reviewed identity")
	}
	after, _ := json.Marshal(v)
	if string(before) != string(after) {
		t.Fatal("projection changed source for a later complete review")
	}
	row := out.Jobs[0]
	if row.Campaign == nil || !row.Campaign.HideUnsubmitted || row.Campaign.Submitted != 2 || inventoryTuples(out)[0][len(inventoryFields)-1] != row.Campaign {
		t.Fatal("quota hint missing from card or global index")
	}
	if row.Job.CreatedAt != at || row.Job.UpdatedAt != at || row.Job.CurrentStatus != "OPEN" || row.Job.Locations[0] != "上海市" || row.Cities[0] != "上海" || row.Local.Role != "backend" || row.Local.Direction.Status != "MATCH" || row.Local.Reasons[0] != "第一条说明" || row.Holistic.Fit != "RELATED" || row.Priority.Score != 75 || row.Application.Version != 2 || row.CompanyPlacement.Rank != 1 || row.Source != "CHATGPT_IMPORT" || row.State != "ANALYZED" || row.Score == nil || *row.Score != score || row.InputKey != "input-key" || row.RequirementsKey != "jd-key" {
		t.Fatal("summary lost filtering, sorting, selection or workflow context", row)
	}
}

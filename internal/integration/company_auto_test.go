package integration

import (
	"encoding/json"
	"strings"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func TestAutoCompanyCandidatesAreReadOnlyPrivateAndDoNotCallModels(t *testing.T) {
	ctx, s, u, token, ids, api, h, m := wholeSetup(t)
	rec := matchingRequest(h, token, "/api/matching/company-auto-candidates", "POST", map[string]any{"company": "Whole Synthetic", "mask_name": "张小明"})
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var result p.AutoCompanySelection
	must(t, json.Unmarshal(rec.Body.Bytes(), &result))
	if result.Total != len(ids) || len(result.Selected) != len(ids) || m.calls.Load() != 0 {
		t.Fatal(result, m.calls.Load())
	}
	if strings.Contains(rec.Body.String(), "private-whole@example.com") || strings.Contains(rec.Body.String(), "张小明") {
		t.Fatal("candidate selection exposed personal material")
	}
	var writes int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM match_runs WHERE user_id=?", u).Scan(&writes))
	if writes != 0 {
		t.Fatal("selection started paid work", writes)
	}
	other, e := s.NewUser(ctx, d.ID()+"@synthetic.test", "test-hash")
	must(t, e)
	t.Cleanup(func() { cleanupFixture(ctx, s, other, "") })
	must(t, s.SaveProfile(ctx, other, d.Profile{Languages: []string{"Go"}}))
	otherToken, e := api.Auth.Token(other)
	must(t, e)
	hidden := matchingRequest(h, otherToken, "/api/matching/company-auto-candidates", "POST", map[string]any{"company": "Whole Synthetic"})
	if hidden.Code != 200 || strings.Contains(hidden.Body.String(), ids[0]) {
		t.Fatal("private company visible", hidden.Code, hidden.Body.String())
	}
	for _, body := range []map[string]any{{"company": ""}, {"company": "Whole Synthetic", "job_ids": ids}} {
		bad := matchingRequest(h, token, "/api/matching/company-auto-candidates", "POST", body)
		if bad.Code != 400 {
			t.Fatal(bad.Code)
		}
	}
}

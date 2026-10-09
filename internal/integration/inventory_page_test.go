package integration

import (
	"encoding/json"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"strings"
	"testing"
	"time"
)

func TestPagedInventoryTracksChangesWithoutCallingModelOrLosingGlobalScope(t *testing.T) {
	ctx, s, u, token, ids, api, h, m := wholeSetup(t)
	type response struct {
		Key   string `json:"snapshot_key"`
		Index struct {
			Full          bool `json:"full"`
			Rows, Upserts [][]any
			Removed       []string
		}
		Jobs []struct {
			Job            struct{ ID string }
			ExcludedReason string `json:"excluded_reason"`
		}
		CandidateHash string `json:"candidate_hash"`
	}
	read := func(known string, page []string) response {
		t.Helper()
		if page == nil {
			page = []string{}
		}
		w := matchingRequest(h, token, "/api/matching/inventory", "POST", map[string]any{"known_snapshot": known, "job_ids": page})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("private response cached")
		}
		var out response
		must(t, json.Unmarshal(w.Body.Bytes(), &out))
		return out
	}
	first := read("", nil)
	if !first.Index.Full || len(first.Index.Rows) < len(ids) || len(first.Jobs) != 0 {
		t.Fatal("initial response must include global index and no unrequested cards")
	}
	same := read(first.Key, ids[:1])
	if same.Key != first.Key || same.Index.Full || len(same.Index.Upserts) != 0 || len(same.Jobs) != 1 || same.Jobs[0].Job.ID != ids[0] {
		t.Fatal("unchanged inventory resent index or failed page read")
	}
	must(t, s.SetPreference(ctx, u, ids[0], "IGNORED"))
	ignored := read(first.Key, ids[:1])
	if ignored.Key == first.Key || ignored.Index.Full || len(ignored.Index.Upserts) != 1 || ignored.Jobs[0].ExcludedReason == "" {
		t.Fatal("preference edit not reflected as one changed row")
	}
	must(t, s.SaveProfile(ctx, u, d.Profile{TargetRoles: []string{"前端开发"}, Languages: []string{"JavaScript"}}))
	profile := read(ignored.Key, ids[:1])
	if profile.Key == ignored.Key || profile.CandidateHash == ignored.CandidateHash {
		t.Fatal("reviewed profile did not invalidate browsing cache")
	}
	in := p.Ingest{Company: "Paged Synthetic", Title: "后端开发", JobType: "FULL_TIME", ExternalID: d.ID(), Locations: []string{"北京市"}, Text: "任职要求：熟悉Go。", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()}
	observation, e := s.IngestForUser(ctx, u, in)
	must(t, e)
	added := read(profile.Key, []string{observation.JobID})
	if len(added.Index.Upserts) != 1 || len(added.Jobs) != 1 {
		t.Fatal("new job not available in global index")
	}
	in.FetchStatus = "BLOCKED"
	in.Text = ""
	in.ObservedAt = in.ObservedAt.Add(time.Second)
	_, e = s.IngestForUser(ctx, u, in)
	must(t, e)
	failed := read(added.Key, []string{observation.JobID})
	if failed.Key == added.Key || failed.Jobs[0].ExcludedReason == "" {
		t.Fatal("latest failed observation hidden by cache")
	}
	// Access to a previous snapshot is scoped to account and model identity.
	other, e := s.NewUser(ctx, d.ID()+"@synthetic.test", "test-hash")
	must(t, e)
	t.Cleanup(func() { cleanupFixture(ctx, s, other, "") })
	must(t, s.SaveProfile(ctx, other, d.Profile{Languages: []string{"Go"}}))
	otherToken, e := api.Auth.Token(other)
	must(t, e)
	stolen := matchingRequest(h, otherToken, "/api/matching/inventory", "POST", map[string]any{"known_snapshot": failed.Key, "job_ids": []string{observation.JobID}})
	var out response
	must(t, json.Unmarshal(stolen.Body.Bytes(), &out))
	if stolen.Code != 200 || !out.Index.Full || len(out.Jobs) != 0 {
		t.Fatal("another account reused a private cached snapshot")
	}
	invalid := matchingRequest(h, token, "/api/matching/inventory", "POST", map[string]any{"job_ids": []string{ids[0], ids[0]}})
	if invalid.Code != 400 {
		t.Fatal("duplicate page IDs accepted")
	}
	if strings.Contains(stolen.Body.String(), "Paged Synthetic") || m.calls.Load() != 0 {
		t.Fatal("private job leak or browsing called a model")
	}
}

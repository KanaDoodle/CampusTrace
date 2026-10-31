package searchservice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	a "github.com/KanaDoodle/CampusTrace/internal/searchapi"
)

// A status reader may publish the candidate while the operator is still
// preparing that same index. Losing CAS must not misreport that success, or
// accept a different operation, source, physical UUID or terminal abort.
func TestRebuildPreparationReconcilesOnlyMatchingPublishedCandidate(t *testing.T) {
	for _, tc := range []struct {
		name, phase string
		change      func(*rebuildRecord)
		conflict    bool
	}{
		{name: "building", phase: "BUILDING"},
		{name: "cutting", phase: "CUTTING"},
		{name: "committed", phase: "COMMITTED"},
		{name: "done", phase: "DONE"},
		{name: "aborted", phase: "ABORTED", conflict: true},
		{name: "different operation", phase: "BUILDING", change: func(r *rebuildRecord) { r.Plan.Operation = bob }, conflict: true},
		{name: "different source", phase: "BUILDING", change: func(r *rebuildRecord) { r.Plan.Source += "changed" }, conflict: true},
		{name: "different UUID", phase: "BUILDING", change: func(r *rebuildRecord) { r.Plan.Target += "changed" }, conflict: true},
		{name: "unpublished target", phase: "BUILDING", change: func(r *rebuildRecord) { r.Plan.Target = "" }, conflict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := rebuildRecord{Plan: a.Rebuild{Operation: alice, Phase: "BUILDING", Mode: "rebuild", Source: "test-cas-source:source-uuid"}, TargetIndex: "test-cas-target", Seq: 4, Term: 1}
			published := original
			published.Plan.Phase = tc.phase
			published.Plan.Target = "test-cas-target:target-uuid"
			published.Plan.Dataset = dataset
			if tc.change != nil {
				tc.change(&published)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == "PUT" && r.URL.Path == "/test-cas-target":
					w.WriteHeader(400) // Already created by the competing preparer.
				case r.Method == "PUT" && r.URL.Path == "/test-cas-control/_doc/rebuild":
					if r.URL.Query().Get("if_seq_no") != "4" || r.URL.Query().Get("if_primary_term") != "1" {
						t.Error("candidate publication lost its CAS fence")
					}
					w.WriteHeader(409)
				case r.Method == "GET" && r.URL.Path == "/test-cas-control/_doc/rebuild":
					_ = json.NewEncoder(w).Encode(map[string]any{"_source": published, "_seq_no": 5, "_primary_term": 1})
				case r.URL.Path == "/_alias/test-cas":
					_, _ = w.Write([]byte(`{"test-cas-source":{}}`))
				case r.URL.Path == "/test-cas-source/_settings":
					_, _ = w.Write([]byte(`{"test-cas-source":{"settings":{"index":{"uuid":"source-uuid"}}}}`))
				case r.URL.Path == "/test-cas-target/_settings":
					_, _ = w.Write([]byte(`{"test-cas-target":{"settings":{"index":{"uuid":"target-uuid"}}}}`))
				default:
					t.Errorf("unexpected OpenSearch request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			backend, err := NewBackend(server.URL, "test-cas")
			if err != nil {
				t.Fatal(err)
			}
			status, err := backend.prepareCandidate(context.Background(), &original)
			if tc.conflict {
				if !errors.Is(err, a.ErrConflict) {
					t.Fatalf("changed rebuild accepted: status=%+v err=%v", status, err)
				}
			} else if err != nil || status.Rebuild == nil || *status.Rebuild != published.Plan {
				t.Fatalf("matching publication reported as failure: status=%+v err=%v", status, err)
			}
		})
	}
}

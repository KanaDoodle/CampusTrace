package integration

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
)

func TestJobDetailRetainsSavedProfileWithoutJobRequirements(t *testing.T) {
	ctx, store, queue, owner, sourceID := setup(t)
	must(t, store.SaveProfile(ctx, owner, d.Profile{
		GraduationYear: 2027, Degree: "MASTER", PreferredTypes: []string{"FULL_TIME", "INTERNSHIP"},
		PreferredCities: []string{"Shanghai"}, AcceptableCities: []string{"Hangzhou"}, Majors: []string{"软件工程"},
	}))
	input := ingest(t, ctx, store, sourceID)
	input.Text = "本科及以上学历"
	observation, err := store.Ingest(ctx, input)
	must(t, err)
	must(t, worker(store, queue).Process(ctx, p.NewTask("ANALYZE", observation.ID)))
	authn := auth.Service{Store: store, Secret: []byte("synthetic-eligibility-test-secret-32")}
	token, err := authn.Token(owner)
	must(t, err)
	handler := (&transport.API{Store: store, Queue: queue, Auth: authn, Metrics: observability.New()}).Handler()
	req := httptest.NewRequest("GET", "/api/jobs/"+observation.JobID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != 200 {
		t.Fatalf("job detail failed: %d %s", response.Code, response.Body.String())
	}
	var detail struct {
		Eligibility d.Eligibility `json:"eligibility"`
	}
	must(t, json.Unmarshal(response.Body.Bytes(), &detail))
	if detail.Eligibility.Status != "UNKNOWN" || detail.Eligibility.RuleVersion != d.RuleVersion {
		t.Fatalf("missing job evidence was incorrectly satisfied: %+v", detail.Eligibility)
	}
	want := map[string]string{
		"GRADUATION_REQUIREMENT": "2027", "EDUCATION_REQUIREMENT": "MASTER", "JOB_TYPE": "FULL_TIME|INTERNSHIP",
		"LOCATION": "Shanghai|Hangzhou", "MAJOR_REQUIREMENT": "软件工程", "EXPERIENCE_REQUIREMENT": "0",
	}
	for _, row := range detail.Eligibility.Results {
		if candidate, ok := want[row.Rule]; ok && row.Candidate != candidate {
			t.Fatalf("API hid the saved profile: %+v", row)
		}
		if row.Rule == "GRADUATION_REQUIREMENT" && row.Requirement != "" {
			t.Fatalf("API invented a missing requirement: %+v", row)
		}
	}
}

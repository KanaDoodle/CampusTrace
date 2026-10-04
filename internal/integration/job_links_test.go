package integration

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
)

func TestJobDetailExposesOnlyVisibleSafeOfficialLink(t *testing.T) {
	ctx, s, q, user, source := setup(t)
	authn := auth.Service{Store: s, Secret: []byte("job-links-test-secret-32-characters")}
	token, err := authn.Token(user)
	must(t, err)
	h := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New()}).Handler()
	for _, link := range []string{"https://careers.example.invalid/role", "javascript:alert(1)", "https://secret:password@careers.example.invalid/role"} {
		o, err := s.Ingest(ctx, p.Ingest{SourceID: source, Company: "Link fixture", Title: "服务端开发", ExternalID: d.ID(), JobType: "FULL_TIME", URL: link, Text: jd, FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
		must(t, err)
		rec := matchingRequest(h, token, "/api/jobs/"+o.JobID, "GET", nil)
		if rec.Code != 200 {
			t.Fatal(rec.Code, rec.Body.String())
		}
		var body struct {
			OfficialURL string `json:"official_url"`
		}
		must(t, json.Unmarshal(rec.Body.Bytes(), &body))
		if link == "https://careers.example.invalid/role" && body.OfficialURL != link {
			t.Fatal("missing official link", body)
		}
		if link != "https://careers.example.invalid/role" && body.OfficialURL != "" {
			t.Fatal("unsafe link exposed", body)
		}
	}
	manual, err := s.IngestForUser(ctx, user, p.Ingest{Company: "Manual link fixture", Title: "开发岗位", JobType: "FULL_TIME", URL: "https://manual.example.invalid/role", Text: jd, FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, err)
	link, err := s.OfficialJobURL(ctx, user, manual.JobID)
	must(t, err)
	if link != "" {
		t.Fatal("manual import labelled official", link)
	}
	privateSource := d.ID()
	must(t, s.SaveSource(ctx, d.Source{ID: privateSource, Name: "Private link fixture", Type: "OFFICIAL", Trust: "OFFICIAL", Visibility: "PRIVATE", OwnerID: user}))
	private, err := s.Ingest(ctx, p.Ingest{SourceID: privateSource, Company: "Private link fixture", Title: "开发岗位", JobType: "FULL_TIME", URL: "https://private.example.invalid/role", Text: jd, FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, err)
	other, err := s.NewUser(ctx, d.ID()+"@link-other.invalid", "unused")
	must(t, err)
	link, err = s.OfficialJobURL(ctx, other, private.JobID)
	must(t, err)
	if link != "" {
		t.Fatal("foreign private link exposed", link)
	}
}

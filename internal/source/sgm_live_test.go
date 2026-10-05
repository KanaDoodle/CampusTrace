package source

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func TestSGMCampusLiveReadOnly(t *testing.T) {
	if os.Getenv("CAMPUS_LIVE_SGM") != "1" {
		t.Skip("explicit official public source verification")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	a := PublicPlatform{}
	preview, err := a.PreviewCampus(ctx, SGMCampusURL)
	if err != nil {
		t.Fatal("preview", err)
	}
	s := d.Source{ID: "sgm-live", Adapter: "sgm", Tenant: "campus", RateLimit: 30}
	refs, err := a.Discover(ctx, s, d.WatchTarget{})
	if err != nil || len(refs) == 0 || len(refs) != preview.Total {
		t.Fatalf("discovery count=%d preview=%d error=%v", len(refs), preview.Total, err)
	}
	for _, ref := range []PostingRef{refs[0], refs[len(refs)-1]} {
		result, err := a.FetchPosting(ctx, s, ref)
		if err != nil || result.Status != "SUCCESS" || !strings.Contains(result.Text, "任职要求") {
			t.Fatalf("detail id=%s status=%s error=%v", ref.ExternalID, result.Status, err)
		}
		if err := (p.Ingest{SourceID: s.ID, ExternalID: ref.ExternalID, URL: ref.URL, Title: ref.Title, Company: ref.Company, JobType: ref.JobType, Locations: ref.Locations, Text: result.Text, FetchStatus: result.Status}).Validate(); err != nil {
			t.Fatal("ingest validation", err)
		}
	}
	t.Logf("official full campus scope=%d first=%s last=%s", len(refs), refs[0].ExternalID, refs[len(refs)-1].ExternalID)
}

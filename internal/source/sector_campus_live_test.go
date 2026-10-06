package source

import (
	"context"
	"os"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

// Explicitly opt in to public, read-only checks. No account, database writes,
// application actions or model requests are involved.
func TestSectorCampusLiveReadOnly(t *testing.T) {
	if os.Getenv("CAMPUS_LIVE_SOURCES") != "1" {
		t.Skip("explicit public read-only verification")
	}
	for _, adapter := range sectorAdapters {
		t.Run(adapter, func(t *testing.T) {
			a := PublicPlatform{}
			s := d.Source{ID: adapter + "-live", Adapter: adapter, Tenant: moreTenant(adapter)}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			refs, err := a.Discover(ctx, s, d.WatchTarget{})
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range refs {
				if err := (p.Ingest{SourceID: s.ID, ExternalID: r.ExternalID, URL: r.URL, Title: r.Title, Company: r.Company, JobType: r.JobType, Locations: r.Locations, Text: "public original", FetchStatus: "SUCCESS"}).Validate(); err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("%s full scope %d", adapter, len(refs))
			if len(refs) == 0 && adapter == "cmbnt" {
				return
			}
			if len(refs) == 0 {
				t.Fatal("unexpected empty live scope")
			}
			for _, r := range []PostingRef{refs[0], refs[len(refs)-1]} {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				res, err := a.FetchPosting(ctx, s, r)
				cancel()
				if err != nil {
					t.Fatalf("detail %s: %v", r.ExternalID, err)
				}
				if res.Status != "SUCCESS" || res.Text == "" {
					t.Fatal("empty original")
				}
				t.Logf("detail %s %s (%d bytes)", r.ExternalID, r.Title, len(res.Text))
			}
		})
	}
}

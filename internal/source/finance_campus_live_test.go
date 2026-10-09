package source

import (
	"context"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"os"
	"testing"
	"time"
)

var financeAdapters = []string{"bankcomm_tech", "cms_securities", "htsc_securities", "csc_securities", "guosen_securities", "galaxy_securities", "cicc_securities"}

func TestFinanceCampusLiveReadOnly(t *testing.T) {
	if os.Getenv("CAMPUS_LIVE_SOURCES") != "1" {
		t.Skip("explicit public read-only verification")
	}
	for _, adapter := range financeAdapters {
		t.Run(adapter, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
			defer cancel()
			a := PublicPlatform{}
			s := d.Source{ID: adapter + "-live", Adapter: adapter, Tenant: moreTenant(adapter)}
			refs, err := a.Discover(ctx, s, d.WatchTarget{})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("complete scope %d", len(refs))
			for _, ref := range refs {
				if err := (p.Ingest{SourceID: s.ID, ExternalID: ref.ExternalID, URL: ref.URL, Title: ref.Title, Company: ref.Company, JobType: ref.JobType, Locations: ref.Locations, Text: "public original", FetchStatus: "SUCCESS"}).Validate(); err != nil {
					t.Fatal(err)
				}
			}
			if len(refs) == 0 {
				return
			}
			for _, ref := range []PostingRef{refs[0], refs[len(refs)-1]} {
				res, err := a.FetchPosting(ctx, s, ref)
				if err != nil || res.Status != "SUCCESS" || res.Text == "" {
					t.Fatalf("detail %s: %v", ref.ExternalID, err)
				}
				t.Logf("detail %s (%d bytes)", ref.ExternalID, len(res.Text))
			}
		})
	}
}

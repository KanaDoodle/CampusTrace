package source

import (
	"context"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"os"
	"testing"
	"time"
)

func TestMoreCampusLiveReadOnly(t *testing.T) {
	if os.Getenv("CAMPUS_LIVE_SOURCES") != "1" {
		t.Skip("explicit public read-only verification")
	}
	for _, adapter := range []string{"h3c", "yusys", "cksic", "whxmc", "neusoft", "mthreads", "nexchip", "lenovo", "midea", "byd", "hikvision", "qihoo360", "sany", "inovance", "vivo", "honor"} {
		t.Run(adapter, func(t *testing.T) {
			a := PublicPlatform{}
			s := d.Source{ID: adapter + "-live", Adapter: adapter, Tenant: moreTenant(adapter)}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			refs, err := a.Discover(ctx, s, d.WatchTarget{})
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			for _, ref := range refs {
				if err := (p.Ingest{SourceID: s.ID, ExternalID: ref.ExternalID, URL: ref.URL, Title: ref.Title, Company: ref.Company, JobType: ref.JobType, Locations: ref.Locations, Text: "public original", FetchStatus: "SUCCESS"}).Validate(); err != nil {
					t.Fatal(err)
				}
			}
			withPlaces := 0
			for _, ref := range refs {
				if len(ref.Locations) > 0 {
					withPlaces++
				}
			}
			t.Logf("%s full scope %d; locations on %d jobs", adapter, len(refs), withPlaces)
			if len(refs) == 0 && adapter == "inovance" {
				return
			}
			if len(refs) == 0 {
				t.Fatal("unexpected empty live scope")
			}
			selected := []PostingRef{refs[0], refs[len(refs)-1]}
			if os.Getenv("CAMPUS_LIVE_ALL_DETAILS") == "1" {
				selected = refs
			}
			for _, ref := range selected {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				res, err := a.FetchPosting(ctx, s, ref)
				cancel()
				if err != nil {
					t.Fatalf("detail %s: %v", ref.ExternalID, err)
				}
				if res.Status != "SUCCESS" {
					t.Fatal(res.Status)
				}
				t.Logf("detail %s %s (%d bytes)", ref.ExternalID, ref.Title, len(res.Text))
			}
		})
	}
}

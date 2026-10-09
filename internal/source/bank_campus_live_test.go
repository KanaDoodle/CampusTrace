package source

import (
	"context"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"os"
	"testing"
	"time"
)

// Opt-in read-only public verification. No candidate credentials or model calls.
func TestBankCampusLiveReadOnly(t *testing.T) {
	if os.Getenv("CAMPUS_LIVE_SOURCES") != "1" {
		t.Skip("explicit public read-only verification")
	}
	for _, adapter := range bankAdapters {
		t.Run(adapter, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
			defer cancel()
			a := PublicPlatform{}
			s := d.Source{ID: adapter + "-live", Adapter: adapter, Tenant: moreTenant(adapter)}
			refs, err := a.Discover(ctx, s, d.WatchTarget{})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s complete scope %d", adapter, len(refs))
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

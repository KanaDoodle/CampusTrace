package integration

import (
	"context"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/source"
	"os"
	"strings"
	"testing"
	"time"
)

// External source drift is deliberately excluded from ordinary CI.
func TestRadarLiveSources(t *testing.T) {
	if os.Getenv("CAMPUS_LIVE_SOURCES") != "1" {
		t.Skip("requires CAMPUS_LIVE_SOURCES=1 and CAMPUS_INTEGRATION=1")
	}
	for _, tc := range []struct{ adapter, tenant, name string }{{"lever", "weride", "WeRide"}, {"greenhouse", "pingcap", "PingCAP"}, {"smartrecruiters", "Ubisoft2", "Ubisoft"}} {
		t.Run(tc.adapter, func(t *testing.T) {
			ctx, s, q, _, id := radarSetup(t)
			src := d.Source{ID: id, Name: tc.name, Adapter: tc.adapter, Tenant: tc.tenant, RateLimit: 30, Trust: "THIRD_PARTY", Type: "THIRD_PARTY", Visibility: "GLOBAL", Timezone: "Asia/Shanghai"}
			_, err := s.DB.ExecContext(ctx, "UPDATE sources SET body=? WHERE id=?", d.JSON(src), id)
			must(t, err)
			a := source.PublicPlatform{Allow: func(ctx context.Context, id string, n int) (bool, error) {
				return q.Allow(ctx, "source:"+id, n, time.Minute)
			}}
			refs, err := a.Discover(ctx, src, d.WatchTarget{})
			must(t, err)
			if len(refs) == 0 {
				t.Fatal("no posting to fetch; discovery alone is not live verification")
			}
			chosen := refs[0]
			for _, ref := range refs {
				location := strings.ToLower(strings.Join(ref.Locations, " "))
				if strings.Contains(location, "shanghai") || strings.Contains(location, "china") || strings.Contains(location, "guangzhou") {
					chosen = ref
					break
				}
			}
			snap, err := a.FetchPosting(ctx, src, chosen)
			must(t, err)
			if snap.Status != "SUCCESS" || snap.Text == "" {
				t.Fatal("no usable snapshot")
			}
			o, err := s.Ingest(ctx, p.Ingest{SourceParserVersion: a.Version(), Company: chosen.Company, Title: chosen.Title, JobType: chosen.JobType, Locations: chosen.Locations, SourceID: id, ExternalID: chosen.ExternalID, URL: chosen.URL, Text: snap.Text, FetchStatus: snap.Status, HTTPStatus: snap.HTTPStatus})
			must(t, err)
			persisted, err := s.Observation(ctx, o.ID)
			must(t, err)
			if persisted.Hash != d.Hash(snap.Text) {
				t.Fatal("snapshot did not enter observation chain")
			}
			t.Logf("LIVE VERIFIED adapter=%s tenant=%s discovered=%d external_id=%s observation=%s url=%s title=%s location=%v", tc.adapter, tc.tenant, len(refs), chosen.ExternalID, o.ID, chosen.URL, chosen.Title, chosen.Locations)
		})
	}
}

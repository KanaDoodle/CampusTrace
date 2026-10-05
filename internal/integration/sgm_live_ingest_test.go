package integration

import (
	"os"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/source"
)

// Opt-in live verification uses only the disposable schema created by verify-radar.sh.
func TestSGMLivePrivateIngest(t *testing.T) {
	if os.Getenv("CAMPUS_LIVE_SGM") != "1" || os.Getenv("CAMPUS_INTEGRATION") != "1" {
		t.Skip("requires live source and isolated integration schema")
	}
	ctx, store, _, owner, _ := radarSetup(t)
	reg, err := store.CreateCampusSource(ctx, owner, "sgm", "campus", "上汽通用/泛亚校招", d.WatchInput{CheckInterval: 3600, Enabled: true})
	must(t, err)
	a := source.PublicPlatform{}
	refs, err := a.Discover(ctx, reg.Source, d.WatchTarget{})
	must(t, err)
	if len(refs) == 0 {
		t.Fatal("official campus category has no posting")
	}
	for _, ref := range []source.PostingRef{refs[0], refs[len(refs)-1]} {
		result, err := a.FetchPosting(ctx, reg.Source, ref)
		must(t, err)
		if result.Status != "SUCCESS" {
			t.Fatal("detail not usable", result.Status)
		}
		in := p.Ingest{SourceParserVersion: a.Version(), SourceID: reg.Source.ID, ExternalID: ref.ExternalID, URL: ref.URL, Title: ref.Title, Company: ref.Company, JobType: ref.JobType, Locations: ref.Locations, Text: result.Text, FetchStatus: result.Status, HTTPStatus: result.HTTPStatus}
		obs, err := store.IngestForUser(ctx, owner, in)
		must(t, err)
		saved, err := store.Observation(ctx, obs.ID)
		must(t, err)
		if saved.Hash != d.Hash(result.Text) || saved.Text != result.Text {
			t.Fatal("official detail not persisted intact")
		}
	}
	page, err := store.SourceJobsForUser(ctx, owner, reg.Source.ID, 1)
	must(t, err)
	if page.Total == 0 || page.Total > len(refs) {
		t.Fatalf("private source jobs=%d discovered=%d", page.Total, len(refs))
	}
	t.Logf("official source=%s discovered=%d persisted=%d first=%s", reg.Source.Adapter, len(refs), page.Total, refs[0].ExternalID)
}

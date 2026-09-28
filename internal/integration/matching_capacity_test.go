package integration

import (
	"fmt"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"testing"
	"time"
)

func TestMatchingScreensBeyondSearchPageAndObservationsInvalidateByContent(t *testing.T) {
	ctx, s, _, u, _ := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{Degree: "MASTER", GraduationYear: 2027, Languages: []string{"Go"}, TargetRoles: []string{"后端开发"}}))
	wanted := map[string]bool{}
	var original p.Ingest
	var first d.Observation
	for i := 0; i < 600; i++ {
		in := p.Ingest{Company: "Synthetic Matching Catalog", Title: fmt.Sprintf("服务端开发 %d", i), JobType: "FULL_TIME", Locations: []string{"上海市"}, ExternalID: d.ID(), Text: "熟悉 Go", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()}
		o, err := s.IngestForUser(ctx, u, in)
		must(t, err)
		wanted[o.JobID] = true
		if i == 0 {
			original = in
			first = o
		}
	}
	model := matching.ModelIdentity("server-default", "fixture")
	snap, err := s.MatchSnapshot(ctx, u, model, "", nil)
	must(t, err)
	for _, row := range snap.Jobs {
		delete(wanted, row.Job.ID)
	}
	if len(wanted) != 0 {
		t.Fatal("catalog stopped at the search page limit", len(wanted))
	}
	before, err := s.MatchSnapshot(ctx, u, model, "", []string{first.JobID})
	must(t, err)
	original.ObservedAt = time.Now().UTC().Add(time.Second)
	_, err = s.IngestForUser(ctx, u, original)
	must(t, err)
	same, err := s.MatchSnapshot(ctx, u, model, "", []string{first.JobID})
	must(t, err)
	if before.Jobs[0].InputKey != same.Jobs[0].InputKey {
		t.Fatal("unchanged text required new paid work")
	}
	original.Text = "熟悉 Go 和 Redis"
	original.ObservedAt = original.ObservedAt.Add(time.Second)
	_, err = s.IngestForUser(ctx, u, original)
	must(t, err)
	changed, err := s.MatchSnapshot(ctx, u, model, "", []string{first.JobID})
	must(t, err)
	if before.Jobs[0].InputKey == changed.Jobs[0].InputKey {
		t.Fatal("changed requirements retained old match")
	}
	original.FetchStatus = "BLOCKED"
	original.Text = ""
	original.ObservedAt = original.ObservedAt.Add(time.Second)
	_, err = s.IngestForUser(ctx, u, original)
	must(t, err)
	blocked, err := s.MatchSnapshot(ctx, u, model, "", []string{first.JobID})
	must(t, err)
	if blocked.Jobs[0].TextBytes != 0 || blocked.Jobs[0].ExcludedReason == "" {
		t.Fatal("blocked latest observation analyzed historical text")
	}
}

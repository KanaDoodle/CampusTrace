package integration

import (
	"fmt"
	"strings"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	a "github.com/KanaDoodle/CampusTrace/internal/searchapi"
)

// Exercise page transitions and the hard scan cap with an authoritative SQL
// catalog. No model tasks are needed for the synthetic extra catalog rows.
func TestSearchFallbackAdaptiveScanKeepsACLOrderAndCap(t *testing.T) {
	ctx, s, _, owner, _ := kafkaSetup(t)
	observation, e := s.IngestForUser(ctx, owner, p.Ingest{Company: "Synthetic pagination", Title: "template", JobType: "FULL_TIME", Locations: []string{"上海"}, Text: "template", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()})
	must(t, e)
	template, e := s.Job(ctx, observation.JobID)
	must(t, e)
	bob, e := s.NewUser(ctx, d.ID()+"@pagination.invalid", "unused")
	must(t, e)
	for offset := 1; offset <= 10050; offset += 100 {
		var placeholders []string
		var args []any
		for i := offset; i < offset+100 && i <= 10050; i++ {
			row := template
			row.ID = fmt.Sprintf("%032x", i)
			row.Title = fmt.Sprintf("row-%d", i)
			row.Visibility = "GLOBAL"
			row.OwnerID = ""
			if i == 601 || i == 9999 {
				row.Title = "needle"
			}
			if i == 650 {
				row.Title = "needle"
				row.Visibility = "PRIVATE"
				row.OwnerID = bob
			}
			if i == 10050 {
				row.Title = "beyondcap"
			}
			row.Fingerprint = d.Hash(row.ID)
			placeholders = append(placeholders, "(?,?,?,?,?,?)")
			args = append(args, row.ID, row.CompanyID, row.Fingerprint, row.Visibility, row.OwnerID, d.JSON(row))
		}
		_, e = s.DB.ExecContext(ctx, "INSERT INTO jobs(id,company_id,fingerprint,visibility,owner_id,body) VALUES"+strings.Join(placeholders, ","), args...)
		must(t, e)
	}
	for _, tc := range []struct {
		text  string
		limit int
		ids   []string
	}{
		{"needle", 1, []string{fmt.Sprintf("%032x", 601)}},
		{"needle", 5, []string{fmt.Sprintf("%032x", 601), fmt.Sprintf("%032x", 9999)}},
		{"beyondcap", 5, []string{}},
		{"", 3, []string{fmt.Sprintf("%032x", 1), fmt.Sprintf("%032x", 2), fmt.Sprintf("%032x", 3)}},
	} {
		out, e := s.SearchJobs(ctx, owner, a.Query{Text: tc.text, Limit: tc.limit})
		must(t, e)
		if out.Mode != "basic" || !out.More || len(out.IDs) != len(tc.ids) {
			t.Fatalf("%+v: %+v", tc, out)
		}
		for i, id := range tc.ids {
			if out.IDs[i] != id || out.Jobs[i].ID != id || out.Jobs[i].Visibility != "GLOBAL" || out.Jobs[i].OwnerID != "" || len(out.Jobs[i].Locations) != 1 || out.Jobs[i].Locations[0] != "上海" {
				t.Fatalf("row data/ACL changed: %+v", out)
			}
		}
	}
}

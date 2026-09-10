package integration

import (
	"context"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/migrations"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRepairLegacyMigration(t *testing.T) {
	dsn := os.Getenv("MYSQL_MIGRATION_TEST_DSN")
	if os.Getenv("CAMPUS_INTEGRATION") != "1" || dsn == "" {
		t.Skip("set MYSQL_MIGRATION_TEST_DSN to an empty disposable database")
	}
	ctx := context.Background()
	s, err := p.Open(ctx, dsn)
	must(t, err)
	defer s.DB.Close()
	var n int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE()").Scan(&n))
	if n != 0 {
		t.Fatal("migration fixture requires an empty database; refuses to overwrite existing data")
	}
	for _, stmt := range strings.Split(migrations.SQL, ";") {
		if strings.TrimSpace(stmt) != "" {
			_, err = s.DB.ExecContext(ctx, stmt)
			must(t, err)
		}
	}
	user, err := s.NewUser(ctx, "migration@test.invalid", "unused")
	must(t, err)
	for _, kind := range []string{"MANUAL", "MANUAL_EMPTY", "OFFICIAL"} {
		trust := kind
		if strings.HasPrefix(kind, "MANUAL") {
			trust = "MANUAL"
		}
		src := d.Source{ID: kind, Name: kind, Type: trust, Trust: trust}
		sourceBody := d.JSON(src)
		if kind == "MANUAL" {
			sourceBody = d.JSON(map[string]string{"id": kind, "name": kind, "type": trust, "trust": trust})
		}
		_, err = s.DB.ExecContext(ctx, "INSERT INTO sources(id,body) VALUES(?,?)", kind, sourceBody)
		must(t, err)
		co := d.Company{ID: kind, Name: kind}
		_, err = s.DB.ExecContext(ctx, "INSERT INTO companies(id,normalized_name,body) VALUES(?,?,?)", kind, kind, d.JSON(co))
		must(t, err)
		j := d.Job{ID: kind, CompanyID: kind, Company: kind, Title: "Backend", JobType: "FULL_TIME", Locations: []string{"Shanghai"}, Fingerprint: d.Hash(kind), UpdatedAt: time.Now().Add(-time.Hour)}
		_, err = s.DB.ExecContext(ctx, "INSERT INTO jobs(id,company_id,fingerprint,body) VALUES(?,?,?,?)", kind, kind, j.Fingerprint, d.JSON(j))
		must(t, err)
		post := d.Posting{ID: kind, JobID: kind, SourceID: kind, ExternalID: "CaseA", LastSeen: j.UpdatedAt}
		_, err = s.DB.ExecContext(ctx, "INSERT INTO postings(id,job_id,source_id,source_key,body) VALUES(?,?,?,?,?)", kind, kind, kind, d.Hash("legacy "+kind), d.JSON(post))
		must(t, err)
		o := d.Observation{ID: kind, PostingID: kind, JobID: kind, ObservedAt: j.UpdatedAt, Text: "preserved raw", FetchStatus: "SUCCESS"}
		_, err = s.DB.ExecContext(ctx, "INSERT INTO observations(id,posting_id,job_id,observed_at,body) VALUES(?,?,?,?,?)", kind, kind, kind, o.ObservedAt, d.JSON(o))
		must(t, err)
	}
	for i := 0; i < 2; i++ {
		must(t, s.Migrate(ctx))
	}
	for _, id := range []string{"MANUAL", "MANUAL_EMPTY"} {
		if _, err = s.JobForUser(ctx, user, id); err == nil {
			t.Fatal("legacy private owner guessed", id)
		}
	}
	if _, err = s.JobForUser(ctx, user, "OFFICIAL"); err != nil {
		t.Fatal("official catalog hidden", err)
	}
	o, err := s.Observation(ctx, "MANUAL")
	must(t, err)
	if o.Text != "preserved raw" {
		t.Fatal("migration changed original content")
	}
	next, err := s.Ingest(ctx, p.Ingest{SourceID: "OFFICIAL", Company: "OFFICIAL", Title: "Backend", JobType: "FULL_TIME", Locations: []string{"Shanghai"}, ExternalID: "CaseA", Text: "new raw", FetchStatus: "SUCCESS"})
	must(t, err)
	if next.PostingID != "OFFICIAL" || next.JobID != "OFFICIAL" {
		t.Fatal("migration broke existing posting lookup")
	}
}

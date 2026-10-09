package persistence

import (
	"context"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/migrations"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRadarUpgradeV01(t *testing.T) {
	dsn := os.Getenv("MYSQL_RADAR_MIGRATION_TEST_DSN")
	if os.Getenv("CAMPUS_INTEGRATION") != "1" || dsn == "" {
		t.Skip("requires an empty MYSQL_RADAR_MIGRATION_TEST_DSN")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	var n int
	if err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE()").Scan(&n); err != nil || n != 0 {
		t.Fatal("requires empty disposable database", n, err)
	}
	for _, stmt := range strings.Split(migrations.SQL, ";") {
		if strings.TrimSpace(stmt) != "" {
			if _, err = s.DB.ExecContext(ctx, stmt); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = s.migrateRepair(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := s.NewUser(ctx, "radar-upgrade@test.invalid", "unused")
	if err != nil {
		t.Fatal(err)
	}
	src := d.Source{ID: d.ID(), Name: "Upgrade", Type: "OFFICIAL", Trust: "OFFICIAL"}
	if err = s.SaveSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	// Seed the historical schema directly: the current write path also emits
	// harness events, whose table deliberately does not exist before upgrade.
	company := d.Company{ID: d.ID(), Name: "Upgrade"}
	job := d.Job{ID: d.ID(), CompanyID: company.ID, Company: company.Name, Title: "Go", JobType: "FULL_TIME", Fingerprint: d.Hash("legacy-upgrade"), Visibility: "GLOBAL"}
	posting := d.Posting{ID: d.ID(), JobID: job.ID, SourceID: src.ID, ExternalID: "CaseA"}
	o := d.Observation{ID: d.ID(), JobID: job.ID, PostingID: posting.ID, Text: "preserved original", Hash: d.Hash("preserved original"), FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()}
	if _, err = s.DB.ExecContext(ctx, "INSERT INTO companies(id,normalized_name,body) VALUES(?,?,?)", company.ID, "upgrade", d.JSON(company)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, "INSERT INTO jobs(id,company_id,fingerprint,body) VALUES(?,?,?,?)", job.ID, company.ID, job.Fingerprint, d.JSON(job)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, "INSERT INTO postings(id,job_id,source_id,source_key,body) VALUES(?,?,?,?,?)", posting.ID, job.ID, src.ID, d.Hash("legacy-posting"), d.JSON(posting)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, "INSERT INTO observations(id,posting_id,job_id,observed_at,body) VALUES(?,?,?,?,?)", o.ID, posting.ID, o.JobID, o.ObservedAt, d.JSON(o)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	kept, err := s.Observation(ctx, o.ID)
	if err != nil || kept.Text != o.Text || kept.JobID != o.JobID {
		t.Fatal("upgrade changed observation", err)
	}
	if err = s.SetPreference(ctx, u, o.JobID, "SAVED"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"watch_targets", "watch_results", "watch_postings", "watch_runs", "notifications", "user_job_preferences", "agent_executions", "agent_connectors", "agent_events", "agent_feed_settings", "agent_todos"} {
		if err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=?", table).Scan(&n); err != nil || n != 1 {
			t.Fatal("missing new schema", table, n, err)
		}
	}
}

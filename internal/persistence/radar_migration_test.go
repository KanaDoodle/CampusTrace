package persistence

import (
	"context"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/migrations"
	"os"
	"strings"
	"testing"
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
	o, err := s.Ingest(ctx, Ingest{Company: "Upgrade", Title: "Go", JobType: "FULL_TIME", ExternalID: "CaseA", SourceID: src.ID, Text: "preserved original", FetchStatus: "SUCCESS"})
	if err != nil {
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
	for _, table := range []string{"watch_targets", "watch_results", "watch_postings", "watch_runs", "notifications", "user_job_preferences"} {
		if err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=?", table).Scan(&n); err != nil || n != 1 {
			t.Fatal("missing new schema", table, n, err)
		}
	}
}

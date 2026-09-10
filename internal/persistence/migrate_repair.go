package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/rules"
	"github.com/KanaDoodle/CampusTrace/migrations"
	"strings"
)

// MySQL DDL commits implicitly. Each DDL step is restartable and the data
// migration/receipt commit together. Serialize concurrent service startups.
func (s *Store) migrateRepair(ctx context.Context) error {
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var locked int
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(CONCAT(DATABASE(),':repair-v2'),30)").Scan(&locked); err != nil {
		return err
	}
	if locked != 1 {
		return fmt.Errorf("migration lock unavailable")
	}
	defer conn.ExecContext(context.Background(), "DO RELEASE_LOCK(CONCAT(DATABASE(),':repair-v2'))")
	if _, err = conn.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations(version VARCHAR(64) PRIMARY KEY)"); err != nil {
		return err
	}
	var done int
	if err = conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version='repair-v2'").Scan(&done); err != nil {
		return err
	}
	if done > 0 {
		if err := migrateIdentity(ctx, conn); err != nil {
			return err
		}
		return migrateRelease(ctx, conn)
	}
	for _, col := range []struct{ name, ddl string }{{"visibility", "VARCHAR(16) NOT NULL DEFAULT 'GLOBAL'"}, {"owner_id", "VARCHAR(32) NOT NULL DEFAULT ''"}} {
		var n int
		if err = conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='jobs' AND column_name=?", col.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err = conn.ExecContext(ctx, "ALTER TABLE jobs ADD COLUMN "+col.name+" "+col.ddl); err != nil {
				return err
			}
		}
	}
	var unique int
	if err = conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='jobs' AND index_name='fingerprint' AND non_unique=0").Scan(&unique); err != nil {
		return err
	}
	if unique > 0 {
		if _, err = conn.ExecContext(ctx, "ALTER TABLE jobs DROP INDEX fingerprint, ADD INDEX fingerprint(fingerprint)"); err != nil {
			return err
		}
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range strings.Split(migrations.VisibilitySQL, ";") {
		if strings.TrimSpace(stmt) != "" {
			if _, err = tx.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version) VALUES('repair-v2')"); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if err := migrateIdentity(ctx, conn); err != nil {
		return err
	}
	return migrateRelease(ctx, conn)
}

// Preserve existing IDs and raw/history rows while upgrading lookup keys. This
// does not attempt to unmerge historical collisions whose true split is unknown.
func migrateIdentity(ctx context.Context, conn *sql.Conn) error {
	var n int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version='identity-v2'").Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	jobs, err := Many[d.Job](ctx, tx, "SELECT body FROM jobs")
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if j.Visibility == "" {
			j.Visibility = "GLOBAL"
		}
		first, e := One[d.Observation](ctx, tx, "SELECT body FROM observations WHERE job_id=? ORDER BY observed_at,id LIMIT 1", j.ID)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e == nil {
			j.Fingerprint = rules.Fingerprint(j.Company, j.Title, j.JobType, j.Locations, first.Text)
		}
		if _, err = tx.ExecContext(ctx, "UPDATE jobs SET fingerprint=?,body=? WHERE id=?", j.Fingerprint, d.JSON(j), j.ID); err != nil {
			return err
		}
	}
	postings, err := Many[d.Posting](ctx, tx, "SELECT body FROM postings")
	if err != nil {
		return err
	}
	for _, post := range postings {
		j, e := One[d.Job](ctx, tx, "SELECT body FROM jobs WHERE id=?", post.JobID)
		if e != nil {
			return e
		}
		first, e := One[d.Observation](ctx, tx, "SELECT body FROM observations WHERE posting_id=? ORDER BY observed_at,id LIMIT 1", post.ID)
		if e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				continue
			}
			return e
		}
		key := SourceKey(Ingest{SourceID: post.SourceID, ExternalID: post.ExternalID, URL: post.URL, Company: j.Company, Title: j.Title, JobType: j.JobType, Locations: j.Locations, Text: first.Text})
		if _, err = tx.ExecContext(ctx, "UPDATE postings SET source_key=? WHERE id=?", key, post.ID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version) VALUES('identity-v2')"); err != nil {
		return err
	}
	return tx.Commit()
}

func migrateRelease(ctx context.Context, conn *sql.Conn) error {
	for _, stmt := range strings.Split(migrations.ReleaseSQL, ";") {
		if strings.TrimSpace(stmt) != "" {
			if _, err := conn.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
	}
	var n int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='chunks' AND index_name='chunks_owner_id'").Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := conn.ExecContext(ctx, "CREATE INDEX chunks_owner_id ON chunks(user_id,id)"); err != nil {
			return err
		}
	}
	_, err := conn.ExecContext(ctx, "INSERT IGNORE INTO schema_migrations(version) VALUES('release-repair-v3')")
	return err
}

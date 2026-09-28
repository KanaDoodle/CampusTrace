package persistence

import (
	"context"
	"fmt"
	"github.com/KanaDoodle/CampusTrace/migrations"
	"strings"
)

func (s *Store) migrateBackend(ctx context.Context) error {
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var locked int
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(CONCAT(DATABASE(),':backend-v1'),30)").Scan(&locked); err != nil {
		return err
	}
	if locked != 1 {
		return fmt.Errorf("backend migration lock unavailable")
	}
	defer conn.ExecContext(context.Background(), "DO RELEASE_LOCK(CONCAT(DATABASE(),':backend-v1'))")
	for _, stmt := range strings.Split(migrations.BackendSQL, ";") {
		if strings.TrimSpace(stmt) != "" {
			if _, err = conn.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
	}
	var exists int
	if err = conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='jobs' AND column_name='company_name'").Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		if _, err = conn.ExecContext(ctx, "ALTER TABLE jobs ADD COLUMN company_name VARCHAR(200) COLLATE utf8mb4_bin GENERATED ALWAYS AS (JSON_UNQUOTE(JSON_EXTRACT(body,'$.company'))) VIRTUAL"); err != nil {
			return err
		}
	}
	if err = conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='jobs' AND index_name='jobs_company_visible'").Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		if _, err = conn.ExecContext(ctx, "ALTER TABLE jobs ADD INDEX jobs_company_visible(company_name,visibility,owner_id,id)"); err != nil {
			return err
		}
	}
	_, err = conn.ExecContext(ctx, "INSERT IGNORE INTO schema_migrations(version) VALUES('backend-v1')")
	return err
}

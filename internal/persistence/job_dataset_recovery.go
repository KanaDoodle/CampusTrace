package persistence

import (
	"context"
	"database/sql"
	"errors"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

// Identity changes are confined to a fresh recovery copy. Historical event and
// notification receipts remain intact; derived Search rows are regenerated.
type JobDatasetRecovery struct {
	Previous   string    `json:"previous_dataset"`
	Current    string    `json:"current_dataset"`
	PreparedAt time.Time `json:"prepared_at"`
}

func (s *Store) JobDatasetRecovery(ctx context.Context) (*JobDatasetRecovery, error) {
	var value JobDatasetRecovery
	err := s.DB.QueryRowContext(ctx, "SELECT previous_dataset,current_dataset,prepared_at FROM job_dataset_recovery WHERE id='jobs'").Scan(&value.Previous, &value.Current, &value.PreparedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &value, err
}

// Existing publisher and sync sessions must finish before recovery resets
// cursors. Locks belong to this connection, not to an unrelated pooled session.
func lockRecoveryProjection(ctx context.Context, db *sql.DB) (*sql.Conn, func(), error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	names := []string{}
	cleanup := func() {
		release, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		for i := len(names) - 1; i >= 0; i-- {
			_, _ = conn.ExecContext(release, "DO RELEASE_LOCK(?)", names[i])
		}
		conn.Close()
	}
	var publisher, builder, syncer string
	if err = conn.QueryRowContext(ctx, "SELECT CONCAT('ct-event:',LEFT(SHA2(DATABASE(),256),32)),CONCAT('ct-search-build:',LEFT(SHA2(DATABASE(),256),32)),CONCAT('ct-search-sync:',LEFT(SHA2(DATABASE(),256),32))").Scan(&publisher, &builder, &syncer); err != nil {
		cleanup()
		return nil, nil, err
	}
	for _, name := range []string{publisher, builder, syncer} {
		var held int
		if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?,0)", name).Scan(&held); err != nil {
			cleanup()
			return nil, nil, err
		}
		if held != 1 {
			cleanup()
			return nil, nil, ErrConflict
		}
		names = append(names, name)
	}
	return conn, cleanup, nil
}
func verifyRecoveryProjectionLocks(ctx context.Context, conn *sql.Conn) error {
	var held int
	err := conn.QueryRowContext(ctx, `SELECT
 IS_USED_LOCK(CONCAT('ct-event:',LEFT(SHA2(DATABASE(),256),32)))=CONNECTION_ID()
 AND IS_USED_LOCK(CONCAT('ct-search-build:',LEFT(SHA2(DATABASE(),256),32)))=CONNECTION_ID()
 AND IS_USED_LOCK(CONCAT('ct-search-sync:',LEFT(SHA2(DATABASE(),256),32)))=CONNECTION_ID()`).Scan(&held)
	if err != nil {
		return err
	}
	if held != 1 {
		return ErrConflict
	}
	return nil
}
func prepareJobDataset(ctx context.Context, tx *sql.Tx, schema string, counts map[string]int) error {
	var current string
	if err := tx.QueryRowContext(ctx, "SELECT dataset FROM search_revision_state WHERE id='jobs' FOR UPDATE").Scan(&current); err != nil {
		return err
	}
	var existingSchema, existingDataset string
	err := tx.QueryRowContext(ctx, "SELECT restore_schema,current_dataset FROM job_dataset_recovery WHERE id='jobs' FOR UPDATE").Scan(&existingSchema, &existingDataset)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && existingSchema == schema {
		if existingDataset != current {
			return ErrConflict
		}
		return nil // Repeating prepare on the same copy never retires its new writes.
	}
	next := d.ID()
	var retired int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM job_event_outbox WHERE dataset=? AND published_at IS NULL", current).Scan(&retired); err != nil {
		return err
	}
	// These are rebuildable projections only. Original jobs, observations,
	// applications, notification history and idempotency receipts are untouched.
	for _, query := range []string{"DELETE FROM search_outbox", "DELETE FROM search_documents", "DELETE FROM search_rebuild_deltas", "UPDATE search_rebuild_state SET operation='',dataset='',source_generation='',target_generation='',phase='IDLE',bootstrap_cursor='',copied=0,verified=0,started_version=0,cutover_version=0,updated_at=UTC_TIMESTAMP(6) WHERE id='jobs'"} {
		if _, err = tx.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE search_revision_state SET dataset=?,reconcile_cursor='',initial_done=FALSE,next_reconcile_at=NULL WHERE id='jobs'", next); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE search_sync_state SET generation='',ready=FALSE,bootstrap_cursor='' WHERE id='jobs'"); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO job_dataset_recovery(id,restore_schema,previous_dataset,current_dataset,prepared_at) VALUES('jobs',?,?,?,UTC_TIMESTAMP(6))
 ON DUPLICATE KEY UPDATE restore_schema=VALUES(restore_schema),previous_dataset=VALUES(previous_dataset),current_dataset=VALUES(current_dataset),prepared_at=VALUES(prepared_at)`, schema, current, next)
	if err == nil {
		counts["rotated_job_dataset"] = 1
		counts["retired_job_events"] = retired
	}
	return err
}

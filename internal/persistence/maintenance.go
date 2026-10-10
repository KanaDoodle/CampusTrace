package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type TableProof struct {
	Rows   int64  `json:"rows"`
	SHA256 string `json:"sha256"`
}
type DatabaseProof struct {
	Tables             map[string]TableProof `json:"tables"`
	Migrations         []string              `json:"migrations"`
	ForeignKeysChecked int                   `json:"foreign_keys_checked"`
}

var sqlIdentifier = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

func quoteIdentifier(v string) (string, error) {
	if !sqlIdentifier.MatchString(v) {
		return "", ErrValidation
	}
	return "`" + v + "`", nil
}

// ProofDatabase is for an offline recovery schema. It never logs row contents.
func (s *Store) ProofDatabase(ctx context.Context) (DatabaseProof, error) {
	out := DatabaseProof{Tables: map[string]TableProof{}}
	tables, err := s.DB.QueryContext(ctx, "SELECT table_name FROM information_schema.tables WHERE table_schema=DATABASE() AND table_type='BASE TABLE' ORDER BY table_name")
	if err != nil {
		return out, err
	}
	var names []string
	for tables.Next() {
		var name string
		if err = tables.Scan(&name); err != nil {
			break
		}
		names = append(names, name)
	}
	rowErr := tables.Err()
	tables.Close()
	if err != nil {
		return out, err
	}
	if rowErr != nil {
		return out, rowErr
	}
	for _, name := range names {
		table, e := quoteIdentifier(name)
		if e != nil {
			return out, e
		}
		pk, e := s.DB.QueryContext(ctx, "SELECT column_name FROM information_schema.key_column_usage WHERE table_schema=DATABASE() AND table_name=? AND constraint_name='PRIMARY' ORDER BY ordinal_position", name)
		if e != nil {
			return out, e
		}
		var order []string
		for pk.Next() {
			var col string
			if e = pk.Scan(&col); e != nil {
				break
			}
			quoted, x := quoteIdentifier(col)
			if x != nil {
				e = x
				break
			}
			order = append(order, quoted)
		}
		pkErr := pk.Err()
		pk.Close()
		if e != nil {
			return out, e
		}
		if pkErr != nil {
			return out, pkErr
		}
		if len(order) == 0 {
			return out, fmt.Errorf("table %s lacks deterministic primary key", name)
		}
		rows, e := s.DB.QueryContext(ctx, "SELECT * FROM "+table+" ORDER BY "+strings.Join(order, ","))
		if e != nil {
			return out, e
		}
		cols, e := rows.Columns()
		if e != nil {
			rows.Close()
			return out, e
		}
		values := make([]any, len(cols))
		pointers := make([]any, len(cols))
		for i := range values {
			pointers[i] = &values[i]
		}
		h := sha256.New()
		header, _ := json.Marshal(cols)
		h.Write(header)
		h.Write([]byte{'\n'})
		var count int64
		for rows.Next() {
			if e = rows.Scan(pointers...); e != nil {
				break
			}
			raw, x := json.Marshal(values)
			if x != nil {
				e = x
				break
			}
			h.Write(raw)
			h.Write([]byte{'\n'})
			count++
		}
		scanErr := rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		if scanErr != nil {
			return out, scanErr
		}
		out.Tables[name] = TableProof{Rows: count, SHA256: hex.EncodeToString(h.Sum(nil))}
	}
	// Dumps disable FK checks while importing; re-enabling alone does not check old rows.
	refs, err := s.DB.QueryContext(ctx, `SELECT table_name,column_name,referenced_table_name,referenced_column_name FROM information_schema.key_column_usage WHERE table_schema=DATABASE() AND referenced_table_name IS NOT NULL ORDER BY table_name,constraint_name,ordinal_position`)
	if err != nil {
		return out, err
	}
	var checks [][4]string
	for refs.Next() {
		var row [4]string
		if err = refs.Scan(&row[0], &row[1], &row[2], &row[3]); err != nil {
			break
		}
		checks = append(checks, row)
	}
	refsErr := refs.Err()
	refs.Close()
	if err != nil {
		return out, err
	}
	if refsErr != nil {
		return out, refsErr
	}
	for _, row := range checks {
		var q [4]string
		for i, v := range row {
			q[i], err = quoteIdentifier(v)
			if err != nil {
				return out, err
			}
		}
		var missing int
		query := "SELECT COUNT(*) FROM " + q[0] + " c LEFT JOIN " + q[2] + " p ON BINARY c." + q[1] + "=BINARY p." + q[3] + " WHERE c." + q[1] + " IS NOT NULL AND p." + q[3] + " IS NULL"
		if err = s.DB.QueryRowContext(ctx, query).Scan(&missing); err != nil {
			return out, err
		}
		if missing > 0 {
			return out, fmt.Errorf("broken reference in %s", row[0])
		}
		out.ForeignKeysChecked++
	}
	if _, ok := out.Tables["schema_migrations"]; ok {
		rows, e := s.DB.QueryContext(ctx, "SELECT version FROM schema_migrations ORDER BY version")
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var v string
			if e = rows.Scan(&v); e != nil {
				break
			}
			out.Migrations = append(out.Migrations, v)
		}
		rowErr := rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		if rowErr != nil {
			return out, rowErr
		}
	}
	if _, ok := out.Tables["assessments"]; ok {
		var n int
		if err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM assessments a JOIN JSON_TABLE(a.body,'$.evidence_ids[*]' COLUMNS(evidence_id VARCHAR(32) PATH '$')) r LEFT JOIN evidence e ON BINARY e.id=BINARY r.evidence_id WHERE e.id IS NULL OR BINARY e.job_id<>BINARY a.job_id`).Scan(&n); err != nil {
			return out, err
		}
		if n > 0 {
			return out, fmt.Errorf("assessment references missing evidence")
		}
	}
	for _, query := range []string{
		"SELECT COUNT(*) FROM observations o JOIN postings p ON BINARY p.id=BINARY o.posting_id LEFT JOIN jobs j ON BINARY j.id=BINARY o.job_id WHERE j.id IS NULL OR BINARY o.job_id<>BINARY p.job_id",
		"SELECT COUNT(*) FROM evidence e JOIN observations o ON BINARY e.observation_id=BINARY o.id WHERE BINARY e.job_id<>BINARY o.job_id",
	} {
		var n int
		if err = s.DB.QueryRowContext(ctx, query).Scan(&n); err != nil {
			return out, err
		}
		if n > 0 {
			return out, fmt.Errorf("observation/evidence job association mismatch")
		}
	}
	if _, ok := out.Tables["project_facts"]; ok {
		var n int
		if err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM project_facts f JOIN projects p ON p.id=f.project_id WHERE BINARY f.user_id<>BINARY p.user_id").Scan(&n); err != nil {
			return out, err
		}
		if n > 0 {
			return out, fmt.Errorf("project fact owner mismatch")
		}
	}
	return out, nil
}

var SupportedMigrations = map[string]bool{"repair-v2": true, "identity-v2": true, "release-repair-v3": true, "job-radar-v4": true, "backend-v1": true, "local-reliability-v1": true, "source-import-v1": true, "assessment-schedule-v1": true}

func CheckMigrations(versions []string) error {
	for _, v := range versions {
		if !SupportedMigrations[v] {
			return fmt.Errorf("unsupported database migration %s", v)
		}
	}
	return nil
}

type RetentionReport struct {
	Before     time.Time        `json:"before"`
	Applied    bool             `json:"applied"`
	Rows       map[string]int64 `json:"rows"`
	BatchLimit int              `json:"batch_limit"`
}

// Core observations, evidence, assessments, changes, results and idempotency
// receipts stay forever. Only expendable terminal diagnostics/transport rows go.
func (s *Store) RetainHistory(ctx context.Context, before time.Time, apply bool) (RetentionReport, error) {
	out := RetentionReport{Before: before, Applied: apply, Rows: map[string]int64{}, BatchLimit: 500}
	if before.After(time.Now().UTC().Add(-30 * 24 * time.Hour)) {
		return out, ErrValidation
	}
	rules := []struct{ name, selectSQL, deleteSQL string }{
		{"match_run_events", `SELECT e.id FROM match_run_events e JOIN match_runs r ON r.id=e.run_id WHERE e.occurred_at<? AND r.updated_at<? AND r.state IN ('COMPLETED','CANCELLED') ORDER BY e.occurred_at,e.id LIMIT 500`, `DELETE e FROM match_run_events e JOIN match_runs r ON r.id=e.run_id WHERE e.id=? AND e.occurred_at<? AND r.updated_at<? AND r.state IN ('COMPLETED','CANCELLED')`},
		{"outbox", `SELECT o.id FROM outbox o JOIN completed_tasks c ON c.id=o.id WHERE o.sent=TRUE AND o.created_at<? AND c.completed_at<? ORDER BY o.created_at,o.id LIMIT 500`, `DELETE o FROM outbox o JOIN completed_tasks c ON c.id=o.id WHERE o.id=? AND o.sent=TRUE AND o.created_at<? AND c.completed_at<?`},
	}
	for _, r := range rules {
		rows, err := s.DB.QueryContext(ctx, r.selectSQL, before, before)
		if err != nil {
			return out, err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				break
			}
			ids = append(ids, id)
		}
		rowErr := rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
		if rowErr != nil {
			return out, rowErr
		}
		out.Rows[r.name] = int64(len(ids))
		if apply {
			out.Rows[r.name] = 0
			for _, id := range ids {
				result, e := s.DB.ExecContext(ctx, r.deleteSQL, id, before, before)
				if e != nil {
					return out, e
				}
				n, e := result.RowsAffected()
				if e != nil {
					return out, e
				}
				out.Rows[r.name] += n
			}
		}
	}
	var cacheRows int64
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM (SELECT source_id FROM source_http_cache WHERE updated_at<UTC_TIMESTAMP(6)-INTERVAL 1 DAY LIMIT 500) expired").Scan(&cacheRows); err != nil {
		return out, err
	}
	out.Rows["expired_source_http_cache"] = cacheRows
	if apply {
		result, err := s.DB.ExecContext(ctx, "DELETE FROM source_http_cache WHERE updated_at<UTC_TIMESTAMP(6)-INTERVAL 1 DAY ORDER BY updated_at,source_id,request_key LIMIT 500")
		if err != nil {
			return out, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return out, err
		}
		out.Rows["expired_source_http_cache"] = n
	}
	return out, nil
}

package persistence

import (
	"context"
	"database/sql"
	"sort"
	"strings"
)

const MaxBulkPreferences = 1000

type BulkPreferenceResult struct {
	Disposition string   `json:"disposition"`
	Updated     []string `json:"updated"`
	Skipped     []string `json:"skipped"`
}

// Each bounded request is atomic and validates access to every ID before any
// write. Saved jobs and application records are protected from bulk cleanup.
// Repeating ignore/restore is idempotent; restoring affects only ignored jobs.
func (s *Store) SetBulkPreference(ctx context.Context, user string, jobs []string, value string) (BulkPreferenceResult, error) {
	v := BulkPreferenceResult{Disposition: value, Updated: []string{}, Skipped: []string{}}
	if user == "" || len(jobs) == 0 || len(jobs) > MaxBulkPreferences || (value != "IGNORED" && value != "NONE") {
		return v, ErrValidation
	}
	ids := append([]string{}, jobs...)
	sort.Strings(ids)
	for i, id := range ids {
		if len(id) != 32 || (i > 0 && ids[i-1] == id) {
			return v, ErrValidation
		}
	}
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		slots := make([]string, len(ids))
		args := []any{user, user, user}
		for i, id := range ids {
			slots[i] = "?"
			args = append(args, id)
		}
		rows, err := tx.QueryContext(ctx, `SELECT j.id, COALESCE(p.disposition,'NONE'),
 EXISTS(SELECT 1 FROM applications a WHERE a.user_id=? AND a.job_id=j.id)
 FROM jobs j LEFT JOIN user_job_preferences p ON p.job_id=j.id AND p.user_id=?
 WHERE (j.visibility='GLOBAL' OR (j.visibility='PRIVATE' AND j.owner_id=?))
 AND j.id IN (`+strings.Join(slots, ",")+`) ORDER BY j.id FOR UPDATE`, args...)
		if err != nil {
			return err
		}
		seen := 0
		for rows.Next() {
			var id, disposition string
			var application bool
			if err = rows.Scan(&id, &disposition, &application); err != nil {
				rows.Close()
				return err
			}
			seen++
			if value == "IGNORED" && (disposition == "SAVED" || application) || value == "NONE" && disposition != "IGNORED" {
				v.Skipped = append(v.Skipped, id)
			} else {
				v.Updated = append(v.Updated, id)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if seen != len(ids) {
			return ErrNotFound
		}
		if len(v.Updated) == 0 {
			return nil
		}
		values := make([]string, len(v.Updated))
		writeArgs := []any{}
		for i, id := range v.Updated {
			values[i] = "(?,?,?)"
			writeArgs = append(writeArgs, user, id, value)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO user_job_preferences(user_id,job_id,disposition) VALUES `+strings.Join(values, ",")+` ON DUPLICATE KEY UPDATE disposition=VALUES(disposition)`, writeArgs...)
		return err
	})
	if err != nil {
		// Never report rolled-back writes as successful.
		return BulkPreferenceResult{Disposition: value, Updated: []string{}, Skipped: []string{}}, err
	}
	return v, nil
}

package persistence

import (
	"context"
	"database/sql"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"regexp"
	"time"
)

// Recovery copies must not start old watches or pretend to retain credentials.
// This method refuses the live schema and never touches results or personal facts.
func (s *Store) PrepareRecovery(ctx context.Context) (map[string]int, error) {
	counts := map[string]int{"paused_watches": 0, "interrupted_matching_tasks": 0}
	var database string
	if err := s.DB.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&database); err != nil {
		return counts, err
	}
	if !regexp.MustCompile(`^campustrace_restore_[a-f0-9]{32}$`).MatchString(database) {
		return counts, ErrValidation
	}
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		watches, err := Many[d.WatchTarget](ctx, tx, "SELECT body FROM watch_targets WHERE enabled=TRUE FOR UPDATE")
		if err != nil {
			return err
		}
		for _, v := range watches {
			v.Enabled = false
			v.ScheduleVersion++
			v.UpdatedAt = time.Now().UTC()
			v.LastOutcome = "RESTORED_PAUSED"
			if err = saveWatch(ctx, tx, v); err != nil {
				return err
			}
			counts["paused_watches"]++
		}
		runs, err := Many[MatchRun](ctx, tx, "SELECT body FROM match_runs WHERE state IN ('RUNNING','PAUSING') FOR UPDATE")
		if err != nil {
			return err
		}
		for _, v := range runs {
			v.State = "WAITING_AUTH"
			for i := range v.Items {
				if v.Items[i].State == "RUNNING" {
					v.Items[i].State = "INTERRUPTED"
					v.Items[i].Code = "MATCH_INTERRUPTED"
				}
			}
			var user string
			if err = tx.QueryRowContext(ctx, "SELECT user_id FROM match_runs WHERE id=?", v.ID).Scan(&user); err != nil {
				return err
			}
			if err = writeRun(ctx, tx, user, &v); err != nil {
				return err
			}
			counts["interrupted_matching_tasks"]++
		}
		if _, err = tx.ExecContext(ctx, `UPDATE source_import_items SET body=JSON_SET(body,'$.state','FAILED','$.code','SOURCE_IMPORT_INTERRUPTED') WHERE watch_id IS NULL AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.state')) IN ('PENDING','PREPARING')`); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE match_runs SET token='',lease_until=NULL")
		return err
	})
	return counts, err
}

package persistence

import (
	"context"
	"database/sql"
	"errors"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

const assessmentLease = 10 * time.Minute

// InitAssessmentSchedule runs once at worker startup, not on each timer tick.
// Existing jobs are backfilled in bounded batches; new jobs register on ingest.
func (s *Store) InitAssessmentSchedule(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, `INSERT IGNORE INTO job_assessment_schedule(job_id,next_assess_at) SELECT id,UTC_TIMESTAMP(6) FROM jobs`); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE job_assessment_schedule SET next_assess_at=UTC_TIMESTAMP(6) WHERE rule_version<>? AND (next_assess_at IS NULL OR next_assess_at>UTC_TIMESTAMP(6))`, d.RuleVersion)
	return err
}

// Caller owns the parent job lock. One outstanding task consumes the newest
// committed inputs; repeated changes coalesce without losing a later change.
func enqueueAssessment(ctx context.Context, tx *sql.Tx, job string) error {
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `INSERT INTO job_assessment_schedule(job_id,next_assess_at) VALUES(?,?) ON DUPLICATE KEY UPDATE next_assess_at=VALUES(next_assess_at)`, job, now); err != nil {
		return err
	}
	var pending sql.NullString
	if err := tx.QueryRowContext(ctx, "SELECT pending_task_id FROM job_assessment_schedule WHERE job_id=? FOR UPDATE", job).Scan(&pending); err != nil {
		return err
	}
	if pending.Valid {
		return nil
	}
	return scheduleAssessment(ctx, tx, job, now)
}

func scheduleAssessment(ctx context.Context, tx *sql.Tx, job string, now time.Time) error {
	t := NewTask("ASSESS", job)
	if err := Outbox(ctx, tx, t); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE job_assessment_schedule SET pending_task_id=?,pending_until=? WHERE job_id=?`, t.ID, now.Add(assessmentLease), job)
	return err
}

// ScheduleAssessments uses the due-time index and locks jobs before schedule
// rows, matching writers. SQL Outbox and the pending lease commit together.
func (s *Store) ScheduleAssessments(ctx context.Context, now time.Time, limit int) (int, error) {
	if now.IsZero() || limit < 1 || limit > 500 {
		return 0, ErrValidation
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT job_id FROM job_assessment_schedule WHERE next_assess_at<=? AND (pending_task_id IS NULL OR pending_until<=?) ORDER BY next_assess_at,job_id LIMIT ?`, now.UTC(), now.UTC(), limit)
	if err != nil {
		return 0, err
	}
	var jobs []string
	for rows.Next() {
		var job string
		if err = rows.Scan(&job); err != nil {
			break
		}
		jobs = append(jobs, job)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	if rowErr != nil {
		return 0, rowErr
	}
	count := 0
	for _, job := range jobs {
		added := false
		err = s.Tx(ctx, func(tx *sql.Tx) error {
			var id string
			if err := tx.QueryRowContext(ctx, "SELECT id FROM jobs WHERE id=? FOR UPDATE", job).Scan(&id); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return err
			}
			var at, until sql.NullTime
			var pending sql.NullString
			if err := tx.QueryRowContext(ctx, `SELECT next_assess_at,pending_task_id,pending_until FROM job_assessment_schedule WHERE job_id=? FOR UPDATE`, job).Scan(&at, &pending, &until); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return err
			}
			if !at.Valid || at.Time.After(now) || (pending.Valid && until.Valid && until.Time.After(now)) {
				return nil
			}
			if err := scheduleAssessment(ctx, tx, job, now.UTC()); err != nil {
				return err
			}
			added = true
			return nil
		})
		if err != nil {
			return count, err
		}
		if added {
			count++
		}
	}
	return count, nil
}

func saveAssessmentSchedule(ctx context.Context, tx *sql.Tx, job, input, task string, at time.Time) error {
	var due any
	if !at.IsZero() {
		due = at
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO job_assessment_schedule(job_id,input_key,rule_version,next_assess_at) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE input_key=VALUES(input_key),rule_version=VALUES(rule_version),next_assess_at=VALUES(next_assess_at)`, job, input, d.RuleVersion, due); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE job_assessment_schedule SET pending_task_id=NULL,pending_until=NULL WHERE job_id=? AND pending_task_id=?`, job, task)
	return err
}

// Kept for callers; the hourly full-catalog scan has been removed.
func (s *Store) EnqueueFreshness(ctx context.Context) error {
	_, err := s.ScheduleAssessments(ctx, time.Now().UTC(), 100)
	return err
}

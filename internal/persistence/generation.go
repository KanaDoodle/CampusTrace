package persistence

import (
	"context"
	"database/sql"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

var ErrProcessingUnavailable = errors.New("503 task processing implementation unavailable")

// All observation/evidence mutations take the parent job lock before its observation.
// Assessment CAS uses the same lock, so no input can change during verification/commit.
func lockAnalysis(ctx context.Context, tx *sql.Tx, id string) (d.Observation, error) {
	o, err := One[d.Observation](ctx, tx, "SELECT body FROM observations WHERE id=?", id)
	if err != nil {
		return o, err
	}
	var job string
	if err = tx.QueryRowContext(ctx, "SELECT id FROM jobs WHERE id=? FOR UPDATE", o.JobID).Scan(&job); err != nil {
		return o, err
	}
	return One[d.Observation](ctx, tx, "SELECT body FROM observations WHERE id=? FOR UPDATE", id)
}

// BindAnalysis is the scheduling boundary: the first request for a new complete
// processing identity allocates and activates a generation. Retries of known
// identities, including previously queued old work, never change that selection.
func (s *Store) BindAnalysis(ctx context.Context, t Task, version string) (Task, error) {
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		o, err := lockAnalysis(ctx, tx, t.EntityID)
		if err != nil {
			return err
		}
		var bound Task
		err = tx.QueryRowContext(ctx, "SELECT observation_id,processing_version,generation FROM analysis_task_generations WHERE task_id=?", t.ID).Scan(&bound.EntityID, &bound.ProcessingVersion, &bound.Generation)
		if err == nil {
			if bound.EntityID != t.EntityID || (t.Generation != 0 && t.Generation != bound.Generation) || (t.ProcessingVersion != "" && t.ProcessingVersion != bound.ProcessingVersion) {
				return ErrValidation
			}
			t.Generation, t.ProcessingVersion = bound.Generation, bound.ProcessingVersion
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if t.ProcessingVersion != "" && t.ProcessingVersion != version {
			return ErrValidation
		}
		var generation uint64
		err = tx.QueryRowContext(ctx, "SELECT generation FROM analysis_generations WHERE observation_id=? AND processing_version=?", o.ID, version).Scan(&generation)
		if errors.Is(err, sql.ErrNoRows) {
			if t.Generation != 0 {
				return ErrValidation
			}
			if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(generation),0)+1 FROM analysis_generations WHERE observation_id=?", o.ID).Scan(&generation); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO analysis_generations(observation_id,processing_version,generation) VALUES(?,?,?)", o.ID, version, generation); err != nil {
				return err
			}
			o.ActiveAnalysisGeneration = generation
			o.DesiredProcessingVersion = version
			o.ExtractionStatus = "PENDING"
			o.ApplySignal = "UNKNOWN"
			o.DeadlineSignal = ""
			o.ErrorCategory = ""
			if _, err = tx.ExecContext(ctx, "UPDATE observations SET body=? WHERE id=?", d.JSON(o), o.ID); err != nil {
				return err
			}
			if err = Outbox(ctx, tx, NewTask("ASSESS", o.JobID)); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if t.Generation != 0 && t.Generation != generation {
			return ErrValidation
		}
		t.Generation, t.ProcessingVersion = generation, version
		_, err = tx.ExecContext(ctx, "INSERT INTO analysis_task_generations(task_id,observation_id,processing_version,generation) VALUES(?,?,?,?)", t.ID, o.ID, version, generation)
		return err
	})
	return t, err
}
func reconcileAnalysis(ctx context.Context, tx *sql.Tx, o d.Observation, version string, generation uint64) error {
	if o.ActiveAnalysisGeneration != generation || o.DesiredProcessingVersion != version {
		return nil
	}
	es, err := Many[d.Evidence](ctx, tx, "SELECT body FROM evidence WHERE observation_id=? ORDER BY id", o.ID)
	if err != nil {
		return err
	}
	var receipt int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM analysis_results WHERE observation_id=? AND analysis_version=?", o.ID, version).Scan(&receipt); err != nil {
		return err
	}
	if receipt == 0 {
		return ErrNotFound
	}
	before := d.JSON(o)
	o.ApplySignal = "UNKNOWN"
	o.DeadlineSignal = ""
	for _, e := range es {
		if e.AnalysisVersion != version {
			continue
		}
		switch e.Type {
		case "APPLY_ACTION":
			o.ApplySignal = e.Value
		case "DEADLINE":
			o.DeadlineSignal = e.Value
		}
	}
	o.AnalysisVersion = version
	o.CurrentAnalysisGeneration = generation
	o.ExtractionStatus = "COMPLETE"
	o.ErrorCategory = ""
	if before == d.JSON(o) {
		return nil
	}
	if _, err = tx.ExecContext(ctx, "UPDATE observations SET body=? WHERE id=?", d.JSON(o), o.ID); err != nil {
		return err
	}
	return Outbox(ctx, tx, NewTask("ASSESS", o.JobID))
}
func (s *Store) ReconcileAnalysis(ctx context.Context, id, version string, generation uint64) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		o, err := lockAnalysis(ctx, tx, id)
		if err != nil {
			return err
		}
		return reconcileAnalysis(ctx, tx, o, version, generation)
	})
}

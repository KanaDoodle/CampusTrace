package persistence

import (
	"context"
	"database/sql"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/rules"
	"sort"
	"time"
)

var ErrStaleInput = errors.New("assessment input changed")

type Evaluation struct {
	InputIdentity string          `json:"input_identity"`
	Job           d.Job           `json:"job"`
	Profile       d.Profile       `json:"profile"`
	Observations  []d.Observation `json:"current_observations"`
	Evidence      []d.Evidence    `json:"current_requirements"`
	Eligibility   d.Eligibility   `json:"eligibility"`
	Ranking       d.Ranking       `json:"ranking"`
	GoFit         string          `json:"go_fit"`
	ValidUntil    time.Time       `json:"valid_until"`
}

func (s *Store) evaluation(ctx context.Context, q Queryer, user, job string, now time.Time) (Evaluation, error) {
	v := Evaluation{}
	if err := CheckJobAccess(ctx, q, user, job); err != nil {
		return v, err
	}
	j, err := One[d.Job](ctx, q, "SELECT body FROM jobs WHERE id=?", job)
	if err != nil {
		return v, err
	}
	profile, err := One[d.Profile](ctx, q, "SELECT body FROM profiles WHERE user_id=?", user)
	if err != nil {
		return v, err
	}
	os, err := Many[d.Observation](ctx, q, "SELECT body FROM observations WHERE job_id=? ORDER BY observed_at,id", job)
	if err != nil {
		return v, err
	}
	es, err := Many[d.Evidence](ctx, q, "SELECT body FROM evidence WHERE job_id=? ORDER BY id", job)
	if err != nil {
		return v, err
	}
	os, es = rules.Current(os, es)
	sort.Slice(es, func(i, j int) bool { return es[i].ID < es[j].ID })
	identity := d.Hash(d.JSON(struct {
		Job          d.Job
		Profile      d.Profile
		Observations []d.Observation
		Evidence     []d.Evidence
		Rule         string
		Weights      rules.Weights
	}{j, profile, os, es, d.RuleVersion, s.Weights}))
	e := rules.Eligibility(j, profile, es, now)
	fit := rules.GoFit(es)
	a := rules.Status(job, os, es, now)
	r := rules.Rank(j, profile, a.Status, e, fit, now, s.Weights)
	e.InputIdentity = identity
	r.InputIdentity = identity
	until := now.Add(time.Minute) // cache bounds the continuously decaying freshness score
	for _, o := range os {
		expires := o.ObservedAt.Add(7 * 24 * time.Hour)
		if expires.After(now) && expires.Before(until) {
			until = expires
		}
		for _, ev := range es {
			if ev.ObservationID == o.ID && ev.Type == "DEADLINE" {
				deadline, err := d.DeadlineInstant(ev.Value, o.Timezone)
				if err == nil && deadline.After(now) && deadline.Before(until) {
					until = deadline
				}
			}
		}
	}
	return Evaluation{identity, j, profile, os, es, e, r, fit, until}, nil
}
func (s *Store) ComputeEvaluation(ctx context.Context, user, job string) (Evaluation, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return Evaluation{}, err
	}
	defer tx.Rollback()
	v, err := s.evaluation(ctx, tx, user, job, time.Now().UTC())
	if err != nil {
		return v, err
	}
	return v, tx.Commit()
}

// PersistAssessment is a separate commit phase. Writers of profile, metadata,
// observations and evidence use these same parent locks. Timestamps never arbitrate CAS.
func (s *Store) PersistAssessment(ctx context.Context, v Evaluation, refresh ...bool) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var id string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", v.Profile.UserID).Scan(&id); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, "SELECT id FROM jobs WHERE id=? FOR UPDATE", v.Job.ID).Scan(&id); err != nil {
			return err
		}
		fresh, err := s.evaluation(ctx, tx, v.Profile.UserID, v.Job.ID, time.Now().UTC())
		if err != nil {
			return err
		}
		if fresh.InputIdentity != v.InputIdentity || !time.Now().Before(v.ValidUntil) {
			return ErrStaleInput
		}
		old, err := One[Evaluation](ctx, tx, "SELECT body FROM evaluation_cache WHERE user_id=? AND job_id=?", v.Profile.UserID, v.Job.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && old.InputIdentity == v.InputIdentity && (len(refresh) == 0 || !refresh[0]) {
			return nil
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO eligibilities(id,user_id,job_id,body) VALUES(?,?,?,?)", v.Eligibility.ID, v.Profile.UserID, v.Job.ID, d.JSON(v.Eligibility)); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO rankings(id,user_id,job_id,body) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE body=VALUES(body)", d.ID(), v.Profile.UserID, v.Job.ID, d.JSON(v.Ranking)); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO evaluation_cache(user_id,job_id,body) VALUES(?,?,?) ON DUPLICATE KEY UPDATE body=VALUES(body)", v.Profile.UserID, v.Job.ID, d.JSON(v))
		return err
	})
}
func (s *Store) QueryEvaluation(ctx context.Context, user, job string) (Evaluation, error) {
	v, err := s.ComputeEvaluation(ctx, user, job)
	if err != nil {
		return v, err
	}
	old, err := One[Evaluation](ctx, s.DB, "SELECT body FROM evaluation_cache WHERE user_id=? AND job_id=?", user, job)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return v, err
	}
	if err == nil && old.InputIdentity == v.InputIdentity && time.Now().Before(old.ValidUntil) {
		return old, nil
	}
	return v, nil // on-demand computation is not an append-only history write
}
func (s *Store) Evaluate(ctx context.Context, user, job string) (d.Eligibility, d.Ranking, string, error) {
	v, err := s.QueryEvaluation(ctx, user, job)
	if err == nil {
		err = s.PersistAssessment(ctx, v)
	}
	return v.Eligibility, v.Ranking, v.GoFit, err
}

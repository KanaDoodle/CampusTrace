package persistence

import (
	"context"
	"database/sql"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"regexp"
	"strings"
	"time"
)

var ErrModelQueueFull = errors.New("global model queue full")
var ErrModelLease = errors.New("global model execution lease lost")
var ErrModelBudget = errors.New("global model daily call budget exhausted")
var ErrModelReview = errors.New("matching call outcome requires human review")
var ErrModelPoolConfig = errors.New("model pool configuration conflicts with authoritative policy")

type ModelPoolPolicy struct {
	ID              string `json:"id"`
	Capacity        int    `json:"capacity"`
	QueueLimit      int    `json:"queue_limit"`
	OwnerQueueLimit int    `json:"owner_queue_limit"`
	DailyLimit      int    `json:"daily_limit"`
	LeaseMS         int    `json:"lease_ms"`
}
type ModelTicket struct {
	ID, PoolID, UserID, State                   string
	Token                                       string `json:"-"`
	Fence                                       uint64
	CreatedAt, WaitUntil, LeaseUntil, HoldUntil time.Time
}
type ModelCall struct {
	ID          string    `json:"id"`
	PoolID      string    `json:"pool_id"`
	RunID       string    `json:"run_id,omitempty"`
	ModelHash   string    `json:"model_hash"`
	Fingerprint string    `json:"fingerprint"`
	State       string    `json:"state"`
	Version     int       `json:"version"`
	UsageDay    string    `json:"usage_day"`
	Code        string    `json:"code,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	ReviewNote  string    `json:"review_note,omitempty"`
	ReviewHash  string    `json:"-"`
}
type ModelPoolStats struct {
	Policy         ModelPoolPolicy `json:"policy"`
	Waiting        int             `json:"waiting"`
	Active         int             `json:"active"`
	Quarantined    int             `json:"quarantined"`
	AllocatedToday int             `json:"allocated_today"`
	Unknown        int             `json:"unknown"`
}

func (s *Store) EnsureModelPool(ctx context.Context, v ModelPoolPolicy) error {
	if !regexp.MustCompile(`^[A-Za-z0-9:_-]{1,64}$`).MatchString(v.ID) || v.Capacity < 1 || v.Capacity > 16 || v.QueueLimit < 1 || v.QueueLimit > 256 || v.OwnerQueueLimit < 1 || v.OwnerQueueLimit > v.QueueLimit || v.DailyLimit < 1 || v.DailyLimit > 100000 || v.LeaseMS < 50 || v.LeaseMS > 60000 {
		return ErrValidation
	}
	_, e := s.DB.ExecContext(ctx, "INSERT IGNORE INTO model_resource_pools(id,capacity,queue_limit,owner_queue_limit,daily_limit,lease_ms) VALUES(?,?,?,?,?,?)", v.ID, v.Capacity, v.QueueLimit, v.OwnerQueueLimit, v.DailyLimit, v.LeaseMS)
	if e != nil {
		return e
	}
	actual, e := modelPool(ctx, s.DB, v.ID, false)
	if e != nil {
		return e
	}
	if actual != v {
		return ErrModelPoolConfig
	}
	return nil
}
func modelPool(ctx context.Context, q Queryer, id string, lock bool) (ModelPoolPolicy, error) {
	v := ModelPoolPolicy{}
	query := "SELECT id,capacity,queue_limit,owner_queue_limit,daily_limit,lease_ms FROM model_resource_pools WHERE id=?"
	if lock {
		query += " FOR UPDATE"
	}
	e := q.QueryRowContext(ctx, query, id).Scan(&v.ID, &v.Capacity, &v.QueueLimit, &v.OwnerQueueLimit, &v.DailyLimit, &v.LeaseMS)
	return v, e
}
func modelClock(ctx context.Context, q Queryer) (time.Time, error) {
	var now time.Time
	e := q.QueryRowContext(ctx, "SELECT UTC_TIMESTAMP(6)").Scan(&now)
	return now, e
}
func modelTicket(ctx context.Context, q Queryer, id string) (ModelTicket, error) {
	v := ModelTicket{}
	e := q.QueryRowContext(ctx, "SELECT id,pool_id,user_id,state,token,fence,created_at,wait_until,lease_until,hold_until FROM model_resource_tickets WHERE id=?", id).Scan(&v.ID, &v.PoolID, &v.UserID, &v.State, &v.Token, &v.Fence, &v.CreatedAt, &v.WaitUntil, &v.LeaseUntil, &v.HoldUntil)
	return v, e
}
func writeModelCall(ctx context.Context, tx *sql.Tx, v *ModelCall) error {
	v.Version++
	_, e := tx.ExecContext(ctx, "UPDATE model_call_ledger SET state=?,version=?,review_hash=?,body=? WHERE id=?", v.State, v.Version, v.ReviewHash, d.JSON(v), v.ID)
	return e
}

// Pool row serializes queue turns, admission and lease transitions across APIs.
// No provider calls or user locks are acquired while holding only this lock.
func expireModelTickets(ctx context.Context, tx *sql.Tx, pool string, now time.Time) error {
	calls, e := Many[ModelCall](ctx, tx, `SELECT c.body FROM model_call_ledger c JOIN model_resource_tickets t ON t.id=c.id WHERE t.pool_id=? AND t.state='CALLING' AND (t.lease_until<=? OR t.hold_until<=?) AND c.state='STARTED'`, pool, now, now)
	if e != nil {
		return e
	}
	for _, v := range calls {
		v.State = "UNKNOWN"
		v.Code = "EXECUTION_LOST"
		v.UpdatedAt = now
		if e = writeModelCall(ctx, tx, &v); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE model_resource_tickets SET state='QUARANTINED' WHERE pool_id=? AND state='CALLING' AND (lease_until<=? OR hold_until<=?)`, pool, now, now); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE model_resource_tickets SET state='RELEASED' WHERE pool_id=? AND state='QUARANTINED' AND hold_until<=?`, pool, now); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE model_resource_tickets SET state='EXPIRED' WHERE pool_id=? AND ((state='QUEUED' AND (wait_until<=? OR lease_until<=?)) OR (state='GRANTED' AND lease_until<=?))`, pool, now, now, now)
	return e
}

func (s *Store) EnqueueModelTicket(ctx context.Context, pool, user string, wait time.Duration) (ModelTicket, error) {
	out := ModelTicket{}
	if wait <= 0 || wait > 10*time.Minute {
		return out, ErrValidation
	}
	e := s.Tx(ctx, func(tx *sql.Tx) error {
		if e := lockRunUser(ctx, tx, user); e != nil {
			return e
		}
		if e := checkMatchRunGuard(ctx, tx, user, "", ""); e != nil {
			return e
		}
		policy, e := modelPool(ctx, tx, pool, true)
		if e != nil {
			return e
		}
		now, e := modelClock(ctx, tx)
		if e != nil {
			return e
		}
		if e = expireModelTickets(ctx, tx, pool, now); e != nil {
			return e
		}
		var total, owned int
		e = tx.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM(user_id=?),0) FROM model_resource_tickets WHERE pool_id=? AND state='QUEUED'", user, pool).Scan(&total, &owned)
		if e != nil {
			return e
		}
		if total >= policy.QueueLimit || owned >= policy.OwnerQueueLimit {
			return ErrModelQueueFull
		}
		out = ModelTicket{ID: d.ID(), PoolID: pool, UserID: user, State: "QUEUED", CreatedAt: now, WaitUntil: now.Add(wait), LeaseUntil: now.Add(time.Duration(policy.LeaseMS) * time.Millisecond), HoldUntil: now.Add(wait)}
		_, e = tx.ExecContext(ctx, `INSERT INTO model_resource_tickets(id,pool_id,user_id,state,created_at,wait_until,lease_until,hold_until) VALUES(?,?,?,?,?,?,?,?)`, out.ID, pool, user, out.State, out.CreatedAt, out.WaitUntil, out.LeaseUntil, out.HoldUntil)
		return e
	})
	return out, e
}
func (s *Store) PollModelTicket(ctx context.Context, id, user string) (ModelTicket, error) {
	out, e := modelTicket(ctx, s.DB, id)
	if e != nil || out.UserID != user {
		if e == nil {
			e = ErrNotFound
		}
		return out, e
	}
	e = s.Tx(ctx, func(tx *sql.Tx) error {
		policy, e := modelPool(ctx, tx, out.PoolID, true)
		if e != nil {
			return e
		}
		now, e := modelClock(ctx, tx)
		if e != nil {
			return e
		}
		if e = expireModelTickets(ctx, tx, out.PoolID, now); e != nil {
			return e
		}
		// Polling is also the waiting process heartbeat; expired waiters cannot revive.
		if _, e = tx.ExecContext(ctx, `UPDATE model_resource_tickets SET lease_until=? WHERE id=? AND user_id=? AND state='QUEUED' AND lease_until>? AND wait_until>?`, now.Add(time.Duration(policy.LeaseMS)*time.Millisecond), id, user, now, now); e != nil {
			return e
		}
		var active int
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM model_resource_tickets WHERE pool_id=? AND state IN ('GRANTED','CALLING','QUARANTINED')`, out.PoolID).Scan(&active); e != nil {
			return e
		}
		if active < policy.Capacity {
			var cursor string
			if e = tx.QueryRowContext(ctx, "SELECT last_owner FROM model_resource_pools WHERE id=?", out.PoolID).Scan(&cursor); e != nil {
				return e
			}
			choose := func(after string) (string, error) {
				var owner string
				e := tx.QueryRowContext(ctx, `SELECT t.user_id FROM model_resource_tickets t WHERE t.pool_id=? AND t.state='QUEUED' AND t.user_id>? AND NOT EXISTS(SELECT 1 FROM model_resource_tickets a WHERE a.pool_id=t.pool_id AND a.user_id=t.user_id AND a.state IN ('GRANTED','CALLING','QUARANTINED')) ORDER BY t.user_id,t.created_at,t.id LIMIT 1`, out.PoolID, after).Scan(&owner)
				return owner, e
			}
			owner, e := choose(cursor)
			if errors.Is(e, sql.ErrNoRows) {
				owner, e = choose("")
			}
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if e == nil {
				var next string
				if e = tx.QueryRowContext(ctx, `SELECT id FROM model_resource_tickets WHERE pool_id=? AND user_id=? AND state='QUEUED' ORDER BY created_at,id LIMIT 1`, out.PoolID, owner).Scan(&next); e != nil {
					return e
				}
				if _, e = tx.ExecContext(ctx, "UPDATE model_resource_pools SET fence=fence+1,last_owner=? WHERE id=?", owner, out.PoolID); e != nil {
					return e
				}
				var fence uint64
				if e = tx.QueryRowContext(ctx, "SELECT fence FROM model_resource_pools WHERE id=?", out.PoolID).Scan(&fence); e != nil {
					return e
				}
				if _, e = tx.ExecContext(ctx, "UPDATE model_resource_tickets SET state='GRANTED',token=?,fence=?,lease_until=? WHERE id=?", d.ID(), fence, now.Add(time.Duration(policy.LeaseMS)*time.Millisecond), next); e != nil {
					return e
				}
			}
		}
		out, e = modelTicket(ctx, tx, id)
		return e
	})
	return out, e
}

func (s *Store) BeginModelCall(ctx context.Context, t ModelTicket, fingerprint, modelHash, runID string, execution, grace time.Duration) (ModelCall, error) {
	out := ModelCall{}
	if len(fingerprint) != 64 || len(modelHash) != 64 || execution <= 0 || execution > 5*time.Minute || grace < 0 || grace > time.Minute {
		return out, ErrValidation
	}
	e := s.Tx(ctx, func(tx *sql.Tx) error {
		if e := lockRunUser(ctx, tx, t.UserID); e != nil {
			return e
		}
		if e := checkMatchRunGuard(ctx, tx, t.UserID, "", ""); e != nil {
			return e
		}
		policy, e := modelPool(ctx, tx, t.PoolID, true)
		if e != nil {
			return e
		}
		now, e := modelClock(ctx, tx)
		if e != nil {
			return e
		}
		if e = expireModelTickets(ctx, tx, t.PoolID, now); e != nil {
			return e
		}
		current, e := modelTicket(ctx, tx, t.ID)
		if e != nil {
			return e
		}
		if current.UserID != t.UserID || current.State != "GRANTED" || current.Token != t.Token || current.Fence != t.Fence || !current.LeaseUntil.After(now) {
			return ErrModelLease
		}
		var unknown int
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM model_call_ledger WHERE pool_id=? AND user_id=? AND state='UNKNOWN'`, t.PoolID, t.UserID).Scan(&unknown); e != nil {
			return e
		}
		if unknown > 0 {
			return ErrModelReview
		}
		// Both caps and the ledger commit atomically; a global rejection cannot
		// consume a user's quota, and an uncertain result never frees it automatically.
		day := matchDay(now)
		if e = reserveMatchCallTx(ctx, tx, t.UserID, now); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "INSERT IGNORE INTO model_pool_daily(pool_id,usage_day,allocated) VALUES(?,?,0)", t.PoolID, day); e != nil {
			return e
		}
		var used int
		if e = tx.QueryRowContext(ctx, "SELECT allocated FROM model_pool_daily WHERE pool_id=? AND usage_day=?", t.PoolID, day).Scan(&used); e != nil {
			return e
		}
		if used >= policy.DailyLimit {
			return ErrModelBudget
		}
		if _, e = tx.ExecContext(ctx, "UPDATE model_pool_daily SET allocated=allocated+1 WHERE pool_id=? AND usage_day=?", t.PoolID, day); e != nil {
			return e
		}
		out = ModelCall{ID: t.ID, PoolID: t.PoolID, RunID: runID, ModelHash: modelHash, Fingerprint: fingerprint, State: "STARTED", Version: 1, UsageDay: day, StartedAt: now, UpdatedAt: now}
		if _, e = tx.ExecContext(ctx, `INSERT INTO model_call_ledger(id,pool_id,user_id,fingerprint,state,version,created_at,body) VALUES(?,?,?,?,?,?,?,?)`, t.ID, t.PoolID, t.UserID, fingerprint, out.State, out.Version, now, d.JSON(out)); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE model_resource_tickets SET state='CALLING',hold_until=?,lease_until=? WHERE id=?", now.Add(execution+grace), now.Add(time.Duration(policy.LeaseMS)*time.Millisecond), t.ID)
		return e
	})
	return out, e
}

func (s *Store) RenewModelCall(ctx context.Context, t ModelTicket) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		policy, e := modelPool(ctx, tx, t.PoolID, true)
		if e != nil {
			return e
		}
		now, e := modelClock(ctx, tx)
		if e != nil {
			return e
		}
		r, e := tx.ExecContext(ctx, `UPDATE model_resource_tickets SET lease_until=? WHERE id=? AND pool_id=? AND user_id=? AND token=? AND fence=? AND state='CALLING' AND lease_until>? AND hold_until>?`, now.Add(time.Duration(policy.LeaseMS)*time.Millisecond), t.ID, t.PoolID, t.UserID, t.Token, t.Fence, now, now)
		if e != nil {
			return e
		}
		n, e := r.RowsAffected()
		if e == nil && n != 1 {
			return ErrModelLease
		}
		return e
	})
}
func (s *Store) FinishModelCall(ctx context.Context, t ModelTicket, state, code string) error {
	if state != "RESPONSE_RECEIVED" && state != "REJECTED" && state != "UNKNOWN" && state != "NOT_SENT" {
		return ErrValidation
	}
	lost := false
	e := s.Tx(ctx, func(tx *sql.Tx) error {
		if e := lockRunUser(ctx, tx, t.UserID); e != nil {
			return e
		}
		if _, e := modelPool(ctx, tx, t.PoolID, true); e != nil {
			return e
		}
		now, e := modelClock(ctx, tx)
		if e != nil {
			return e
		}
		if e = expireModelTickets(ctx, tx, t.PoolID, now); e != nil {
			return e
		}
		current, e := modelTicket(ctx, tx, t.ID)
		if e != nil {
			return e
		}
		if current.UserID != t.UserID || current.Token != t.Token || current.Fence != t.Fence {
			return ErrModelLease
		}
		call, e := One[ModelCall](ctx, tx, "SELECT body FROM model_call_ledger WHERE id=? AND user_id=?", t.ID, t.UserID)
		if e != nil {
			return e
		}
		if call.State != "STARTED" {
			lost = true
			return nil
		}
		if current.State != "CALLING" || !current.LeaseUntil.After(now) {
			lost = true
			return nil
		}
		call.State = state
		call.Code = code
		call.UpdatedAt = now
		if state == "NOT_SENT" {
			if e = refundModelCall(ctx, tx, t.UserID, call); e != nil {
				return e
			}
		}
		if e = writeModelCall(ctx, tx, &call); e != nil {
			return e
		}
		ticketState := "RELEASED"
		if state == "UNKNOWN" {
			ticketState = "QUARANTINED"
		}
		_, e = tx.ExecContext(ctx, "UPDATE model_resource_tickets SET state=? WHERE id=?", ticketState, t.ID)
		return e
	})
	if e == nil && lost {
		return ErrModelLease
	}
	return e
}
func (s *Store) CancelModelTicket(ctx context.Context, t ModelTicket) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := modelPool(ctx, tx, t.PoolID, true); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `UPDATE model_resource_tickets SET state='CANCELLED' WHERE id=? AND user_id=? AND state IN ('QUEUED','GRANTED') AND (token='' OR token=? OR ?='')`, t.ID, t.UserID, t.Token, t.Token)
		return e
	})
}
func (s *Store) SweepModelPool(ctx context.Context, pool string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := modelPool(ctx, tx, pool, true); e != nil {
			return e
		}
		now, e := modelClock(ctx, tx)
		if e != nil {
			return e
		}
		return expireModelTickets(ctx, tx, pool, now)
	})
}
func (s *Store) ModelCalls(ctx context.Context, user, pool string) ([]ModelCall, error) {
	if e := s.SweepModelPool(ctx, pool); e != nil {
		return nil, e
	}
	return Many[ModelCall](ctx, s.DB, "SELECT body FROM model_call_ledger WHERE user_id=? AND pool_id=? ORDER BY created_at DESC,id DESC LIMIT 100", user, pool)
}
func (s *Store) ModelPoolStatus(ctx context.Context, pool string) (ModelPoolStats, error) {
	if e := s.SweepModelPool(ctx, pool); e != nil {
		return ModelPoolStats{}, e
	}
	return s.ModelPoolMetrics(ctx, pool)
}

// Scraping observes stored aggregate state without expiring or changing calls.
// Lease expiry is reconciled by normal controller/status/review operations.
func (s *Store) ModelPoolMetrics(ctx context.Context, pool string) (ModelPoolStats, error) {
	out := ModelPoolStats{}
	policy, e := modelPool(ctx, s.DB, pool, false)
	if e != nil {
		return out, e
	}
	out.Policy = policy
	e = s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(state='QUEUED'),0),COALESCE(SUM(state IN ('GRANTED','CALLING','QUARANTINED')),0),COALESCE(SUM(state='QUARANTINED'),0) FROM model_resource_tickets WHERE pool_id=? AND state IN ('QUEUED','GRANTED','CALLING','QUARANTINED')`, pool).Scan(&out.Waiting, &out.Active, &out.Quarantined)
	if e != nil {
		return out, e
	}
	now, e := modelClock(ctx, s.DB)
	if e != nil {
		return out, e
	}
	e = s.DB.QueryRowContext(ctx, "SELECT allocated FROM model_pool_daily WHERE pool_id=? AND usage_day=?", pool, matchDay(now)).Scan(&out.AllocatedToday)
	if errors.Is(e, sql.ErrNoRows) {
		e = nil
	}
	if e == nil {
		e = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM model_call_ledger c JOIN model_resource_tickets t ON t.id=c.id WHERE c.pool_id=? AND (c.state='UNKNOWN' OR (c.state='STARTED' AND (t.lease_until<=UTC_TIMESTAMP(6) OR t.hold_until<=UTC_TIMESTAMP(6))))`, pool).Scan(&out.Unknown)
	}
	return out, e
}

type ModelCallReview struct {
	Version  int    `json:"version"`
	Decision string `json:"decision"`
	Note     string `json:"note"`
	Verified bool   `json:"verified"`
}

func refundModelCall(ctx context.Context, tx *sql.Tx, user string, call ModelCall) error {
	r, e := tx.ExecContext(ctx, "UPDATE model_pool_daily SET allocated=allocated-1 WHERE pool_id=? AND usage_day=? AND allocated>0", call.PoolID, call.UsageDay)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	r, e = tx.ExecContext(ctx, "UPDATE match_daily_usage SET calls=calls-1 WHERE user_id=? AND usage_day=? AND calls>0", user, call.UsageDay)
	if e != nil {
		return e
	}
	n, e = r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) ReviewModelCall(ctx context.Context, user, pool, id string, in ModelCallReview) (ModelCall, error) {
	out := ModelCall{}
	in.Note = strings.TrimSpace(in.Note)
	if len(id) != 32 || in.Version < 1 || !in.Verified || len([]rune(in.Note)) < 10 || len(in.Note) > 600 || (in.Decision != "USED" && in.Decision != "NOT_SENT") {
		return out, ErrValidation
	}
	hash := d.Hash(d.JSON(in))
	e := s.Tx(ctx, func(tx *sql.Tx) error {
		if e := lockRunUser(ctx, tx, user); e != nil {
			return e
		}
		if _, e := modelPool(ctx, tx, pool, true); e != nil {
			return e
		}
		now, e := modelClock(ctx, tx)
		if e != nil {
			return e
		}
		if e = expireModelTickets(ctx, tx, pool, now); e != nil {
			return e
		}
		out, e = One[ModelCall](ctx, tx, "SELECT body FROM model_call_ledger WHERE id=? AND user_id=? AND pool_id=?", id, user, pool)
		if e != nil {
			return e
		}
		if e = tx.QueryRowContext(ctx, "SELECT review_hash FROM model_call_ledger WHERE id=?", id).Scan(&out.ReviewHash); e != nil {
			return e
		}
		if out.ReviewHash == hash {
			return nil
		}
		if out.State != "UNKNOWN" || out.Version != in.Version {
			return ErrConflict
		}
		if in.Decision == "NOT_SENT" {
			r, e := tx.ExecContext(ctx, "UPDATE model_pool_daily SET allocated=allocated-1 WHERE pool_id=? AND usage_day=? AND allocated>0", pool, out.UsageDay)
			if e != nil {
				return e
			}
			n, e := r.RowsAffected()
			if e != nil {
				return e
			}
			if n != 1 {
				return ErrConflict
			}
			r, e = tx.ExecContext(ctx, "UPDATE match_daily_usage SET calls=calls-1 WHERE user_id=? AND usage_day=? AND calls>0", user, out.UsageDay)
			if e != nil {
				return e
			}
			n, e = r.RowsAffected()
			if e != nil {
				return e
			}
			if n != 1 {
				return ErrConflict
			}
		}
		out.State = "REVIEWED_" + in.Decision
		out.ReviewHash = hash
		out.ReviewNote = in.Note
		out.UpdatedAt = now
		return writeModelCall(ctx, tx, &out)
	})
	return out, e
}

func (s *Store) CheckModelCallReview(ctx context.Context, user, pool, fingerprint string) error {
	if e := s.SweepModelPool(ctx, pool); e != nil {
		return e
	}
	var n int
	e := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM model_call_ledger WHERE user_id=? AND pool_id=? AND state='UNKNOWN'", user, pool).Scan(&n)
	if e == nil && n > 0 {
		return ErrModelReview
	}
	return e
}
func (s *Store) ModelRunPhase(ctx context.Context, user, phase string, until time.Time) error {
	g, ok := ctx.Value(matchRunGuardKey{}).(MatchRunGuard)
	if !ok {
		return nil
	}
	paused := false
	e := s.UpdateMatchRun(ctx, user, g, func(v *MatchRun) error {
		if v.State == "PAUSING" {
			v.State = "PAUSED"
			paused = true
			return nil
		}
		if v.State != "RUNNING" {
			return ErrMatchRunLease
		}
		v.ExecutionState = phase
		if phase == "QUEUED" {
			v.QueueUntil = &until
			v.ExecutionUntil = nil
		} else {
			v.QueueUntil = nil
			v.ExecutionUntil = &until
		}
		return nil
	})
	if e == nil && paused {
		return ErrMatchRunLease
	}
	return e
}

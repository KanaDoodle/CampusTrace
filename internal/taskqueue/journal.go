// Package taskqueue delivers durable background tasks over Kafka. MySQL owns
// admission, retry schedules and execution leases; Kafka owns message delivery.
package taskqueue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

var (
	ErrFull     = errors.New("durable task queue is full")
	ErrLease    = errors.New("task execution lease expired or changed")
	ErrPending  = errors.New("task is being executed by another worker")
	ErrConflict = errors.New("task identity or delivery version conflict")
)

type Message struct {
	ID    string          `json:"id"`
	Lane  string          `json:"lane"`
	Owner string          `json:"owner"`
	Body  json.RawMessage `json:"body"`
}

type envelope struct {
	Schema    int     `json:"schema"`
	Namespace string  `json:"namespace"`
	Epoch     string  `json:"epoch"`
	Revision  uint64  `json:"revision"`
	Message   Message `json:"message"`
}

type stored struct {
	Message
	Revision           uint64
	State, Token, Hash string
	LeaseUntil         sql.NullTime
}

// Receipt is an execution fence, never an authorization token for a user API.
type Receipt struct {
	ID, Token string
	Revision  uint64
}

type Outcome struct {
	State    string // DONE, RETRY or DEAD; RETRY carries the next validated envelope.
	Body     json.RawMessage
	Delay    time.Duration
	Category string
}

type LaneStats struct {
	Lane          string `json:"lane"`
	Outstanding   int64  `json:"outstanding"`
	InFlight      int64  `json:"inflight"`
	Ready         int64  `json:"ready"`
	Owners        int64  `json:"owners"`
	Capacity      int64  `json:"capacity"`
	OwnerCapacity int64  `json:"owner_capacity"`
}

func (b *Broker) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, e := b.cfg.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit()
}

func (b *Broker) Init(ctx context.Context) error {
	if _, e := b.cfg.DB.ExecContext(ctx, `INSERT IGNORE INTO task_queue_namespaces(namespace_id,epoch) VALUES(?,?)`, b.namespace, d.ID()); e != nil {
		return e
	}
	for _, lane := range b.cfg.Lanes {
		if _, e := b.cfg.DB.ExecContext(ctx, `INSERT INTO task_queue_lanes(namespace_id,lane,capacity,owner_capacity) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE capacity=VALUES(capacity),owner_capacity=VALUES(owner_capacity)`, b.namespace, lane, b.cfg.Capacity, b.cfg.OwnerCapacity); e != nil {
			return e
		}
	}
	return nil
}

func (b *Broker) epoch(ctx context.Context) (string, error) {
	var value string
	e := b.cfg.DB.QueryRowContext(ctx, `SELECT epoch FROM task_queue_namespaces WHERE namespace_id=?`, b.namespace).Scan(&value)
	return value, e
}

func (b *Broker) valid(m Message) bool {
	return b.lanes[m.Lane] && len(m.ID) > 0 && len(m.ID) <= 100 && len(m.Owner) <= 32 && len(m.Body) > 0 && len(m.Body) <= 65536 && json.Valid(m.Body)
}

func owner(v string) string {
	if v == "" {
		return "system"
	}
	return v
}

func (b *Broker) lockLane(ctx context.Context, tx *sql.Tx, lane string) (int, int, error) {
	var capacity, perOwner int
	e := tx.QueryRowContext(ctx, `SELECT capacity,owner_capacity FROM task_queue_lanes WHERE namespace_id=? AND lane=? FOR UPDATE`, b.namespace, lane).Scan(&capacity, &perOwner)
	return capacity, perOwner, e
}

func (b *Broker) admit(ctx context.Context, tx *sql.Tx, m Message, capacity, perOwner int) error {
	var total, mine int
	e := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(owner_id=?),0) FROM kafka_tasks WHERE namespace_id=? AND lane=? AND state NOT IN ('DONE','DEAD')`, owner(m.Owner), b.namespace, m.Lane).Scan(&total, &mine)
	if e != nil {
		return e
	}
	if total >= capacity || mine >= perOwner {
		return ErrFull
	}
	return nil
}

// Enqueue is a durable SQL handoff. The source outbox can be marked sent after
// this succeeds, even if Kafka is unavailable; DispatchOnce resumes publication.
func (b *Broker) Enqueue(ctx context.Context, m Message) error {
	if !b.valid(m) {
		return fmt.Errorf("invalid task message")
	}
	return b.tx(ctx, func(tx *sql.Tx) error {
		capacity, perOwner, e := b.lockLane(ctx, tx, m.Lane)
		if e != nil {
			return e
		}
		var lane, existingOwner string
		e = tx.QueryRowContext(ctx, `SELECT lane,owner_id FROM kafka_tasks WHERE namespace_id=? AND task_id=?`, b.namespace, m.ID).Scan(&lane, &existingOwner)
		if e == nil {
			if lane != m.Lane || existingOwner != owner(m.Owner) {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e = b.admit(ctx, tx, m, capacity, perOwner); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO kafka_tasks(namespace_id,task_id,lane,owner_id,state,body,body_hash) VALUES(?,?,?,?,'READY',?,?)`, b.namespace, m.ID, m.Lane, owner(m.Owner), []byte(m.Body), d.Hash(string(m.Body)))
		return e
	})
}

func readTask(ctx context.Context, tx *sql.Tx, namespace, id string) (v stored, e error) {
	e = tx.QueryRowContext(ctx, `SELECT task_id,lane,owner_id,body,body_hash,revision,state,lease_token,lease_until FROM kafka_tasks WHERE namespace_id=? AND task_id=? FOR UPDATE`, namespace, id).Scan(&v.ID, &v.Lane, &v.Owner, &v.Body, &v.Hash, &v.Revision, &v.State, &v.Token, &v.LeaseUntil)
	if v.Owner == "system" {
		v.Owner = ""
	}
	return
}

func (b *Broker) claim(ctx context.Context, in envelope) (receipt Receipt, complete bool, err error) {
	err = b.tx(ctx, func(tx *sql.Tx) error {
		// The epoch is rechecked inside the transaction, including after restore.
		var epoch string
		if e := tx.QueryRowContext(ctx, `SELECT epoch FROM task_queue_namespaces WHERE namespace_id=? FOR SHARE`, b.namespace).Scan(&epoch); e != nil {
			return e
		}
		if in.Epoch != epoch {
			complete = true
			return nil
		}
		v, e := readTask(ctx, tx, b.namespace, in.Message.ID)
		if errors.Is(e, sql.ErrNoRows) {
			return ErrConflict
		}
		if e != nil {
			return e
		}
		if in.Revision < v.Revision {
			complete = true
			return nil
		}
		if in.Revision != v.Revision || v.Lane != in.Message.Lane || v.Owner != in.Message.Owner || v.Hash != d.Hash(string(in.Message.Body)) {
			return ErrConflict
		}
		if v.State == "DONE" || v.State == "DEAD" {
			complete = true
			return nil
		}
		var available bool
		if e = tx.QueryRowContext(ctx, `SELECT (state IN ('READY','SENDING','SENT') OR (state='RUNNING' AND lease_until<=UTC_TIMESTAMP(6))) FROM kafka_tasks WHERE namespace_id=? AND task_id=?`, b.namespace, v.ID).Scan(&available); e != nil {
			return e
		}
		if !available {
			return ErrPending
		}
		receipt = Receipt{ID: v.ID, Revision: v.Revision, Token: d.ID()}
		_, e = tx.ExecContext(ctx, `UPDATE kafka_tasks SET state='RUNNING',lease_token=?,lease_until=TIMESTAMPADD(MICROSECOND,?,UTC_TIMESTAMP(6)),updated_at=UTC_TIMESTAMP(6) WHERE namespace_id=? AND task_id=?`, receipt.Token, b.Lease().Microseconds(), b.namespace, v.ID)
		return e
	})
	return
}

func (b *Broker) finish(ctx context.Context, r Receipt, out Outcome) error {
	if out.State != "DONE" && out.State != "RETRY" && out.State != "DEAD" {
		return fmt.Errorf("invalid task outcome")
	}
	if out.Delay < 0 || out.Delay > time.Hour || len(out.Category) > 32 {
		return fmt.Errorf("invalid task retry")
	}
	return b.tx(ctx, func(tx *sql.Tx) error {
		v, e := readTask(ctx, tx, b.namespace, r.ID)
		if e != nil {
			return e
		}
		if v.State == out.State && v.Token == r.Token && v.Revision == r.Revision {
			return nil
		}
		var active bool
		if e = tx.QueryRowContext(ctx, `SELECT lease_until>UTC_TIMESTAMP(6) FROM kafka_tasks WHERE namespace_id=? AND task_id=? AND state='RUNNING' AND revision=? AND lease_token=?`, b.namespace, r.ID, r.Revision, r.Token).Scan(&active); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return ErrLease
			}
			return e
		}
		if !active {
			return ErrLease
		}
		body := v.Body
		revision := v.Revision
		if out.State == "RETRY" || (out.State == "DEAD" && len(out.Body) > 0) {
			candidate := v.Message
			candidate.Body = out.Body
			if !b.valid(candidate) {
				return fmt.Errorf("invalid retry body")
			}
			if b.cfg.Validate != nil {
				if e := b.cfg.Validate(candidate); e != nil {
					return e
				}
			}
			body = out.Body
			if out.State == "RETRY" {
				revision++
			}
		}
		_, e = tx.ExecContext(ctx, `UPDATE kafka_tasks SET state=?,body=?,body_hash=?,revision=?,category=?,due_at=TIMESTAMPADD(MICROSECOND,?,UTC_TIMESTAMP(6)),lease_until=NULL,updated_at=UTC_TIMESTAMP(6) WHERE namespace_id=? AND task_id=?`, out.State, []byte(body), d.Hash(string(body)), revision, out.Category, out.Delay.Microseconds(), b.namespace, r.ID)
		return e
	})
}

func (b *Broker) Stats(ctx context.Context) ([]LaneStats, error) {
	out := []LaneStats{}
	for _, lane := range b.cfg.Lanes {
		v := LaneStats{Lane: lane}
		if e := b.cfg.DB.QueryRowContext(ctx, `SELECT capacity,owner_capacity FROM task_queue_lanes WHERE namespace_id=? AND lane=?`, b.namespace, lane).Scan(&v.Capacity, &v.OwnerCapacity); e != nil {
			return nil, e
		}
		if e := b.cfg.DB.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(state='RUNNING'),0),COUNT(DISTINCT owner_id) FROM kafka_tasks WHERE namespace_id=? AND lane=? AND state NOT IN ('DONE','DEAD')`, b.namespace, lane).Scan(&v.Outstanding, &v.InFlight, &v.Owners); e != nil {
			return nil, e
		}
		v.Ready = v.Outstanding - v.InFlight
		out = append(out, v)
	}
	return out, nil
}

func (b *Broker) DeadLetters(ctx context.Context) (map[string]json.RawMessage, error) {
	rows, e := b.cfg.DB.QueryContext(ctx, `SELECT task_id,body,category,created_at,updated_at FROM kafka_tasks WHERE namespace_id=? AND state='DEAD' ORDER BY updated_at LIMIT 10000`, b.namespace)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var id, category string
		var body json.RawMessage
		var first, last time.Time
		if e = rows.Scan(&id, &body, &category, &first, &last); e != nil {
			return nil, e
		}
		raw, _ := json.Marshal(map[string]any{"task": body, "error_category": category, "first_failed": first, "last_failed": last})
		out[id] = raw
	}
	return out, rows.Err()
}

func (b *Broker) DeadTask(ctx context.Context, id string) (body json.RawMessage, revision uint64, err error) {
	err = b.cfg.DB.QueryRowContext(ctx, `SELECT body,revision FROM kafka_tasks WHERE namespace_id=? AND task_id=? AND state='DEAD'`, b.namespace, id).Scan(&body, &revision)
	return
}
func (b *Broker) Redrive(ctx context.Context, id string, expected uint64, body json.RawMessage) error {
	var lane string
	if e := b.cfg.DB.QueryRowContext(ctx, `SELECT lane FROM kafka_tasks WHERE namespace_id=? AND task_id=?`, b.namespace, id).Scan(&lane); e != nil {
		return e
	}
	return b.tx(ctx, func(tx *sql.Tx) error {
		capacity, perOwner, e := b.lockLane(ctx, tx, lane)
		if e != nil {
			return e
		}
		v, e := readTask(ctx, tx, b.namespace, id)
		if e != nil {
			return e
		}
		if v.State != "DEAD" || v.Revision != expected {
			return ErrConflict
		}
		v.Body = body
		if !b.valid(v.Message) {
			return fmt.Errorf("invalid redrive body")
		}
		if e = b.admit(ctx, tx, v.Message, capacity, perOwner); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE kafka_tasks SET state='READY',body=?,body_hash=?,revision=revision+1,lease_token='',lease_until=NULL,category='',dlq_published=FALSE,due_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE namespace_id=? AND task_id=?`, []byte(body), d.Hash(string(body)), b.namespace, id)
		return e
	})
}

func (b *Broker) Trim(ctx context.Context, retention time.Duration) (int64, error) {
	if retention < time.Hour || retention > 365*24*time.Hour {
		return 0, fmt.Errorf("invalid task history retention")
	}
	r, e := b.cfg.DB.ExecContext(ctx, `DELETE FROM kafka_tasks WHERE namespace_id=? AND state='DONE' AND updated_at<TIMESTAMPADD(MICROSECOND,?,UTC_TIMESTAMP(6)) LIMIT 1000`, b.namespace, -retention.Microseconds())
	if e != nil {
		return 0, e
	}
	return r.RowsAffected()
}

func (b *Broker) SetLimits(ctx context.Context, capacity, perOwner int) error {
	if capacity < 1 || capacity > 4096 || perOwner < 1 || perOwner > capacity {
		return fmt.Errorf("invalid task capacity")
	}
	_, e := b.cfg.DB.ExecContext(ctx, `UPDATE task_queue_lanes SET capacity=?,owner_capacity=? WHERE namespace_id=?`, capacity, perOwner, b.namespace)
	return e
}

// ImportLegacy preserves already admitted work without applying new capacity
// limits. It is used only by the explicit stopped-writer upgrade command.
func (b *Broker) ImportLegacy(ctx context.Context, m Message, state string, due time.Time, category string) error {
	if !b.valid(m) || (state != "READY" && state != "RETRY" && state != "DEAD") || len(category) > 32 {
		return fmt.Errorf("invalid legacy task")
	}
	if b.cfg.Validate != nil {
		if e := b.cfg.Validate(m); e != nil {
			return e
		}
	}
	if due.IsZero() {
		due = time.Now().UTC()
	}
	_, e := b.cfg.DB.ExecContext(ctx, `INSERT IGNORE INTO kafka_tasks(namespace_id,task_id,lane,owner_id,state,body,body_hash,due_at,category) VALUES(?,?,?,?,?,?,?,?,?)`, b.namespace, m.ID, m.Lane, owner(m.Owner), state, []byte(m.Body), d.Hash(string(m.Body)), due.UTC(), category)
	return e
}
func (b *Broker) LegacyImported(ctx context.Context) (bool, error) {
	var done bool
	e := b.cfg.DB.QueryRowContext(ctx, `SELECT legacy_imported FROM task_queue_namespaces WHERE namespace_id=?`, b.namespace).Scan(&done)
	return done, e
}
func (b *Broker) MarkLegacyImported(ctx context.Context) error {
	_, e := b.cfg.DB.ExecContext(ctx, `UPDATE task_queue_namespaces SET legacy_imported=TRUE WHERE namespace_id=?`, b.namespace)
	return e
}
func (b *Broker) ImportPoison(ctx context.Context, origin, raw string) error {
	key := d.Hash("legacy:" + origin + ":" + d.Hash(raw))
	body, _ := json.Marshal(map[string]any{"category": "INVALID_LEGACY_TASK", "origin_hash": d.Hash(origin), "body_hash": d.Hash(raw), "bytes": len(raw)})
	_, e := b.cfg.DB.ExecContext(ctx, `INSERT IGNORE INTO kafka_task_poison(namespace_id,record_key,body) VALUES(?,?,?)`, b.namespace, key, body)
	return e
}

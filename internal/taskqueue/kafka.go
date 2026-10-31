package taskqueue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/eventbus"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"log/slog"
)

type Config struct {
	Metrics                 *observability.Metrics
	DB                      *sql.DB
	Brokers                 []string
	Prefix, Namespace       string
	Lanes                   []string
	Capacity, OwnerCapacity int
	Lease                   time.Duration
	Validate                func(Message) error
}
type Broker struct {
	lease     atomic.Int64
	cfg       Config
	namespace string
	lanes     map[string]bool
	client    *kgo.Client
}

func New(cfg Config) (*Broker, error) {
	if cfg.DB == nil || len(cfg.Brokers) == 0 || cfg.Namespace == "" || cfg.Lease <= 0 || cfg.Lease > 10*time.Minute || cfg.Capacity < 1 || cfg.Capacity > 4096 || cfg.OwnerCapacity < 1 || cfg.OwnerCapacity > cfg.Capacity {
		return nil, fmt.Errorf("invalid Kafka task configuration")
	}
	name := regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
	if !name.MatchString(cfg.Prefix) || len(cfg.Prefix) > 120 {
		return nil, fmt.Errorf("invalid Kafka task topic prefix")
	}
	lanes := map[string]bool{}
	for _, lane := range cfg.Lanes {
		if !name.MatchString(lane) || len(lane) > 32 || lanes[lane] {
			return nil, fmt.Errorf("invalid task lane")
		}
		lanes[lane] = true
	}
	if len(lanes) == 0 {
		return nil, fmt.Errorf("task lanes are required")
	}
	if err := eventbus.ValidateBrokers(cfg.Brokers); err != nil {
		return nil, err
	}

	client, e := kgo.NewClient(kgo.SeedBrokers(cfg.Brokers...), kgo.ClientID("campustrace-task-publisher"), kgo.RequiredAcks(kgo.AllISRAcks()), kgo.MaxProduceRequestsInflightPerBroker(1), kgo.ProducerLinger(10*time.Millisecond), kgo.MaxBufferedRecords(128), kgo.MaxBufferedBytes(8<<20), kgo.RecordDeliveryTimeout(5*time.Second), kgo.DialTimeout(2*time.Second), kgo.RequestTimeoutOverhead(2*time.Second))
	if e != nil {
		return nil, e
	}
	b := &Broker{cfg: cfg, namespace: d.Hash(cfg.Namespace), lanes: lanes, client: client}
	b.lease.Store(int64(cfg.Lease))
	return b, nil
}
func (b *Broker) metric(name string) {
	if b.cfg.Metrics != nil {
		b.cfg.Metrics.Add(name, 1)
	}
}
func (b *Broker) Close()                   { b.client.Close() }
func (b *Broker) Topic(lane string) string { return b.cfg.Prefix + "." + strings.ToLower(lane) + ".v1" }
func (b *Broker) DLQTopic() string         { return b.cfg.Prefix + ".dlq.v1" }
func (b *Broker) EnsureTopics(ctx context.Context) error {
	request := kmsg.NewPtrCreateTopicsRequest()
	topics := []string{b.DLQTopic()}
	for _, lane := range b.cfg.Lanes {
		topics = append(topics, b.Topic(lane))
	}
	for _, topic := range topics {
		t := kmsg.NewCreateTopicsRequestTopic()
		t.Topic = topic
		t.NumPartitions = 3
		t.ReplicationFactor = 1
		for name, value := range map[string]string{"retention.ms": "604800000", "max.message.bytes": "1048576", "cleanup.policy": "delete"} {
			c := kmsg.NewCreateTopicsRequestTopicConfig()
			c.Name = name
			c.Value = &value
			t.Configs = append(t.Configs, c)
		}
		request.Topics = append(request.Topics, t)
	}
	response, e := b.client.Request(ctx, request)
	if e != nil {
		return e
	}
	for _, t := range response.(*kmsg.CreateTopicsResponse).Topics {
		if t.ErrorCode != 0 && t.ErrorCode != 36 {
			return fmt.Errorf("Kafka task topic creation failed (%d)", t.ErrorCode)
		}
	}
	return nil
}

// reserve advances one owner at a time in each lane. The SQL transaction ends
// before Kafka I/O; the publication lease makes interrupted sends retryable.
func (b *Broker) reserve(ctx context.Context, lane string) (v stored, found bool, err error) {
	err = b.tx(ctx, func(tx *sql.Tx) error {
		var last string
		e := tx.QueryRowContext(ctx, `SELECT last_owner FROM task_queue_lanes WHERE namespace_id=? AND lane=? FOR UPDATE SKIP LOCKED`, b.namespace, lane).Scan(&last)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		ready := `namespace_id=? AND lane=? AND ((state IN ('READY','RETRY') AND due_at<=UTC_TIMESTAMP(6)) OR (state IN ('SENDING','RUNNING') AND lease_until<=UTC_TIMESTAMP(6)) OR (state='SENT' AND updated_at<TIMESTAMPADD(SECOND,-60,UTC_TIMESTAMP(6))))`
		var id string
		for _, cursor := range []string{last, ""} {
			e = tx.QueryRowContext(ctx, `SELECT task_id FROM kafka_tasks WHERE `+ready+` AND owner_id>? ORDER BY owner_id,due_at,task_id LIMIT 1 FOR UPDATE SKIP LOCKED`, b.namespace, lane, cursor).Scan(&id)
			if e == nil {
				break
			}
			if !errors.Is(e, sql.ErrNoRows) {
				return e
			}
		}
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		v, e = readTask(ctx, tx, b.namespace, id)
		if e != nil {
			return e
		}
		v.Token = d.ID()
		_, e = tx.ExecContext(ctx, `UPDATE kafka_tasks SET state='SENDING',lease_token=?,lease_until=TIMESTAMPADD(SECOND,15,UTC_TIMESTAMP(6)),updated_at=UTC_TIMESTAMP(6) WHERE namespace_id=? AND task_id=?`, v.Token, b.namespace, id)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE task_queue_lanes SET last_owner=? WHERE namespace_id=? AND lane=?`, owner(v.Owner), b.namespace, lane)
		found = e == nil
		return e
	})
	return
}

func (b *Broker) DispatchOnce(ctx context.Context) (int, error) {
	epoch, e := b.epoch(ctx)
	if e != nil {
		return 0, e
	}
	count := 0
	for round := 0; round < 8; round++ {
		work := false
		for _, lane := range b.cfg.Lanes {
			v, found, e := b.reserve(ctx, lane)
			if e != nil {
				return count, e
			}
			if !found {
				continue
			}
			work = true
			value, _ := json.Marshal(envelope{Schema: 1, Namespace: b.namespace, Epoch: epoch, Revision: v.Revision, Message: v.Message})
			e = b.client.ProduceSync(ctx, &kgo.Record{Topic: b.Topic(lane), Key: []byte(b.namespace + ":" + v.ID), Value: value}).FirstErr()
			if e != nil {
				b.metric("task_publish_errors_total")
				// If this update fails, the finite publication lease is still recoverable.
				release, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
				_, _ = b.cfg.DB.ExecContext(release, `UPDATE kafka_tasks SET state='READY',lease_until=NULL WHERE namespace_id=? AND task_id=? AND state='SENDING' AND lease_token=?`, b.namespace, v.ID, v.Token)
				stop()
				return count, e
			}
			_, e = b.cfg.DB.ExecContext(ctx, `UPDATE kafka_tasks SET state='SENT',lease_until=NULL,updated_at=UTC_TIMESTAMP(6) WHERE namespace_id=? AND task_id=? AND state='SENDING' AND lease_token=?`, b.namespace, v.ID, v.Token)
			if e != nil {
				return count, e
			}
			b.metric("task_published_total")
			count++
		}
		if !work {
			break
		}
	}
	n, e := b.publishDead(ctx)
	return count + n, e
}

// Only metadata enters Kafka's dead-letter topic. The authorized operations
// endpoint reads the validated task envelope from the SQL journal.
func (b *Broker) publishDead(ctx context.Context) (int, error) {
	rows, e := b.cfg.DB.QueryContext(ctx, `SELECT task_id,revision,category,body_hash FROM kafka_tasks WHERE namespace_id=? AND state='DEAD' AND dlq_published=FALSE LIMIT 32`, b.namespace)
	if e != nil {
		return 0, e
	}
	type item struct {
		ID             string
		Revision       uint64
		Category, Hash string
	}
	items := []item{}
	for rows.Next() {
		var v item
		if e = rows.Scan(&v.ID, &v.Revision, &v.Category, &v.Hash); e != nil {
			rows.Close()
			return 0, e
		}
		items = append(items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return 0, e
	}
	for i, v := range items {
		body, _ := json.Marshal(map[string]any{"namespace": b.namespace, "task_id": v.ID, "revision": v.Revision, "category": v.Category, "body_hash": v.Hash})
		if e = b.client.ProduceSync(ctx, &kgo.Record{Topic: b.DLQTopic(), Key: []byte(b.namespace + ":" + v.ID), Value: body}).FirstErr(); e != nil {
			return i, e
		}
		if _, e = b.cfg.DB.ExecContext(ctx, `UPDATE kafka_tasks SET dlq_published=TRUE WHERE namespace_id=? AND task_id=? AND revision=? AND state='DEAD'`, b.namespace, v.ID, v.Revision); e != nil {
			return i, e
		}
	}
	return len(items), nil
}

func (b *Broker) poison(ctx context.Context, r *kgo.Record) error {
	key := d.Hash(fmt.Sprintf("%s/%d/%d/%s", r.Topic, r.Partition, r.Offset, d.Hash(string(r.Value))))
	body, _ := json.Marshal(map[string]any{"category": "INVALID_TASK_RECORD", "topic": r.Topic, "partition": r.Partition, "offset": r.Offset, "bytes": len(r.Value), "body_hash": d.Hash(string(r.Value))})
	if e := b.client.ProduceSync(ctx, &kgo.Record{Topic: b.DLQTopic(), Key: []byte(key), Value: body}).FirstErr(); e != nil {
		return e
	}
	_, e := b.cfg.DB.ExecContext(ctx, `INSERT IGNORE INTO kafka_task_poison(namespace_id,record_key,body) VALUES(?,?,?)`, b.namespace, key, body)
	if e == nil {
		b.metric("poison_count")
	}
	return e
}

type Handler func(context.Context, Message) (Outcome, error)

// Consume commits one record only after its business result and next state have
// been persisted. A failed handler/settlement closes this session, preserving the
// committed boundary through process exits and group rebalances.
func (b *Broker) Consume(ctx context.Context, lane string, timeout time.Duration, handle Handler) error {
	if !b.lanes[lane] || timeout <= 0 || timeout >= b.Lease() {
		return fmt.Errorf("invalid task consumer budget")
	}
	for ctx.Err() == nil {
		epoch, e := b.epoch(ctx)
		if e == nil {
			e = b.session(ctx, lane, epoch, timeout, handle)
		}
		if ctx.Err() != nil {
			return nil
		}
		if e != nil {
			b.metric("task_consumer_errors_total")
			slog.Warn("task consumer session will retry", "lane", lane)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
	return nil
}
func (b *Broker) session(ctx context.Context, lane, epoch string, timeout time.Duration, handle Handler) error {
	client, e := kgo.NewClient(kgo.SeedBrokers(b.cfg.Brokers...), kgo.ClientID("campustrace-task-consumer"), kgo.ConsumerGroup("ct-task-"+b.namespace+"-"+epoch+"-"+lane), kgo.ConsumeTopics(b.Topic(lane)), kgo.DisableAutoCommit(), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()), kgo.BlockRebalanceOnPoll(), kgo.FetchMaxBytes(4<<20), kgo.FetchMaxPartitionBytes(1<<20), kgo.SessionTimeout(10*time.Second), kgo.HeartbeatInterval(2*time.Second), kgo.RebalanceTimeout(b.Lease()+30*time.Second))
	if e != nil {
		return e
	}
	defer client.Close()
	for ctx.Err() == nil {
		poll, cancel := context.WithTimeout(ctx, time.Second)
		fetch := client.PollRecords(poll, 1)
		cancel()
		if e := fetch.Err(); e != nil && !errors.Is(e, context.DeadlineExceeded) {
			client.AllowRebalance()
			return e
		}
		current, e := b.epoch(ctx)
		if e != nil || current != epoch {
			client.AllowRebalance()
			return e
		}
		for _, r := range fetch.Records() {
			if e = b.record(ctx, lane, epoch, timeout, r, handle); e != nil {
				client.AllowRebalance()
				return e
			}
			commit, stop := context.WithTimeout(ctx, 5*time.Second)
			e = client.CommitRecords(commit, r)
			stop()
			if e != nil {
				client.AllowRebalance()
				return e
			}
		}
		client.AllowRebalance()
	}
	return ctx.Err()
}
func (b *Broker) record(ctx context.Context, lane, epoch string, timeout time.Duration, r *kgo.Record, handle Handler) error {
	var in envelope
	// Topic sharing across isolated deployments is safe: every namespace has its
	// own group and ignores other namespaces without executing their tasks.
	if len(r.Value) > 1<<20 || d.Strict(r.Value, &in) != nil || in.Schema != 1 || in.Revision == 0 {
		return b.poison(ctx, r)
	}
	if in.Namespace != b.namespace || in.Epoch != epoch {
		return nil
	}
	if !b.valid(in.Message) || in.Message.Lane != lane || string(r.Key) != b.namespace+":"+in.Message.ID {
		return b.poison(ctx, r)
	}
	if b.cfg.Validate != nil {
		if e := b.cfg.Validate(in.Message); e != nil {
			return b.poison(ctx, r)
		}
	}
	receipt, complete, e := b.claim(ctx, in)
	if errors.Is(e, ErrConflict) {
		return b.poison(ctx, r)
	}
	if e != nil || complete {
		return e
	}
	task, cancel := context.WithTimeout(ctx, timeout)
	out, e := handle(task, in.Message)
	cancel()
	if e != nil {
		return e
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	settle, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	if e = b.finish(settle, receipt, out); e != nil {
		b.metric("task_settlement_errors_total")
		return e
	}
	switch out.State {
	case "DONE":
		b.metric("tasks_processed")
	case "RETRY":
		b.metric("retry_count")
	case "DEAD":
		b.metric("dlq_count")
	}
	return nil
}

func (b *Broker) Lease() time.Duration { return time.Duration(b.lease.Load()) }
func (b *Broker) SetLease(lease time.Duration) error {
	if lease <= 0 || lease > 10*time.Minute {
		return fmt.Errorf("invalid task lease")
	}
	b.lease.Store(int64(lease))
	return nil
}

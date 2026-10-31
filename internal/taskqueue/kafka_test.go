package taskqueue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/testsupport/sqlfixture"
	"github.com/twmb/franz-go/pkg/kgo"
	"net/http/httptest"
)

func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func fixture(t *testing.T) (context.Context, *Broker) {
	t.Helper()
	if os.Getenv("CAMPUS_INTEGRATION") != "1" || os.Getenv("KAFKA_TEST_BROKERS") == "" {
		t.Skip("requires owned MySQL and Kafka")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	dsn := sqlfixture.Database(t, os.Getenv("MYSQL_TEST_DSN"), "kafka_tasks")
	s, e := p.Open(ctx, dsn)
	must(t, e)
	t.Cleanup(func() { s.DB.Close() })
	must(t, s.Migrate(ctx))
	b, e := New(Config{Metrics: observability.New(), DB: s.DB, Brokers: strings.Split(os.Getenv("KAFKA_TEST_BROKERS"), ","), Prefix: "test.tasks." + d.ID(), Namespace: d.ID(), Lanes: []string{"ASSESS", "ANALYZE"}, Capacity: 6, OwnerCapacity: 3, Lease: time.Second})
	must(t, e)
	t.Cleanup(b.Close)
	must(t, b.Init(ctx))
	must(t, b.EnsureTopics(ctx))
	return ctx, b
}
func message(owner string) Message {
	return Message{ID: d.ID(), Lane: "ASSESS", Owner: owner, Body: json.RawMessage(`{"value":1}`)}
}
func delivery(t *testing.T, ctx context.Context, b *Broker, m Message) envelope {
	t.Helper()
	epoch, e := b.epoch(ctx)
	must(t, e)
	var revision uint64
	must(t, b.cfg.DB.QueryRowContext(ctx, "SELECT revision FROM kafka_tasks WHERE namespace_id=? AND task_id=?", b.namespace, m.ID).Scan(&revision))
	return envelope{Schema: 1, Namespace: b.namespace, Epoch: epoch, Revision: revision, Message: m}
}
func state(t *testing.T, ctx context.Context, b *Broker, id string) string {
	t.Helper()
	var v string
	must(t, b.cfg.DB.QueryRowContext(ctx, "SELECT state FROM kafka_tasks WHERE namespace_id=? AND task_id=?", b.namespace, id).Scan(&v))
	return v
}
func wait(t *testing.T, ctx context.Context, fn func() bool) {
	t.Helper()
	for !fn() {
		select {
		case <-ctx.Done():
			t.Fatal("task condition timed out")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestJournalCapacityFairnessAndConcurrentAdmission(t *testing.T) {
	ctx, b := fixture(t)
	owner := d.ID()
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := b.Enqueue(ctx, message(owner))
			if e == nil {
				accepted.Add(1)
			} else if !errors.Is(e, ErrFull) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 3 {
		t.Fatal("owner budget", accepted.Load())
	}
	second := message(d.ID())
	must(t, b.Enqueue(ctx, second))
	must(t, b.Enqueue(ctx, second))
	var owners []string
	for i := 0; i < 4; i++ {
		v, ok, e := b.reserve(ctx, "ASSESS")
		must(t, e)
		if !ok {
			t.Fatal("missing reservation")
		}
		owners = append(owners, v.Owner)
	}
	if owners[0] == owners[1] {
		t.Fatal("owner round robin lost", owners)
	}
	independent := message(owner)
	independent.Lane = "ANALYZE"
	must(t, b.Enqueue(ctx, independent))
	stats, e := b.Stats(ctx)
	must(t, e)
	if stats[0].Outstanding != 4 || stats[1].Outstanding != 1 {
		t.Fatal(stats)
	}
}

func TestJournalRetryLeaseDeadLetterRedriveAndRestoreFences(t *testing.T) {
	ctx, b := fixture(t)
	m := message(d.ID())
	must(t, b.Enqueue(ctx, m))
	in := delivery(t, ctx, b, m)
	r, done, e := b.claim(ctx, in)
	must(t, e)
	if done {
		t.Fatal("premature completion")
	}
	if _, _, e = b.claim(ctx, in); !errors.Is(e, ErrPending) {
		t.Fatal("concurrent execution", e)
	}
	altered := in
	altered.Message.Body = json.RawMessage(`{"value":2}`)
	if _, _, e = b.claim(ctx, altered); !errors.Is(e, ErrConflict) {
		t.Fatal("modified record accepted", e)
	}
	// Force the database lease to expire; a stale executor cannot settle it.
	_, e = b.cfg.DB.ExecContext(ctx, "UPDATE kafka_tasks SET lease_until=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE task_id=?", m.ID)
	must(t, e)
	replacement, _, e := b.claim(ctx, in)
	must(t, e)
	if e = b.finish(ctx, r, Outcome{State: "DONE"}); !errors.Is(e, ErrLease) {
		t.Fatal("stale settlement", e)
	}
	next := json.RawMessage(`{"value":3}`)
	must(t, b.finish(ctx, replacement, Outcome{State: "RETRY", Body: next, Delay: time.Hour, Category: "TRANSIENT"}))
	if _, found, e := b.reserve(ctx, m.Lane); e != nil || found {
		t.Fatal("retry ran before due", found, e)
	}
	if _, done, e = b.claim(ctx, in); e != nil || !done {
		t.Fatal("old delivery revived retry", done, e)
	}
	_, e = b.cfg.DB.ExecContext(ctx, "UPDATE kafka_tasks SET due_at=UTC_TIMESTAMP(6) WHERE task_id=?", m.ID)
	must(t, e)
	_, found, e := b.reserve(ctx, m.Lane)
	must(t, e)
	if !found {
		t.Fatal("due retry not reserved")
	}
	m.Body = next
	in = delivery(t, ctx, b, m)
	r, _, e = b.claim(ctx, in)
	must(t, e)
	must(t, b.finish(ctx, r, Outcome{State: "DEAD", Category: "PERMANENT"}))
	dlq, e := b.DeadLetters(ctx)
	must(t, e)
	if len(dlq) != 1 {
		t.Fatal(dlq)
	}
	must(t, b.Redrive(ctx, m.ID, in.Revision, json.RawMessage(`{"value":4}`)))
	if _, done, e = b.claim(ctx, in); e != nil || !done {
		t.Fatal("pre-redrive record executed", done, e)
	}
	m.Body = json.RawMessage(`{"value":4}`)
	in = delivery(t, ctx, b, m)
	_, e = b.cfg.DB.ExecContext(ctx, "UPDATE task_queue_namespaces SET epoch=? WHERE namespace_id=?", d.ID(), b.namespace)
	must(t, e)
	if _, done, e = b.claim(ctx, in); e != nil || !done {
		t.Fatal("old restore epoch executed", done, e)
	}
	in = delivery(t, ctx, b, m)
	r, _, e = b.claim(ctx, in)
	must(t, e)
	must(t, b.finish(ctx, r, Outcome{State: "DONE"}))
	must(t, b.finish(ctx, r, Outcome{State: "DONE"}))
}

func TestJournalCleanupPreservesUnfinishedAndDeadTasks(t *testing.T) {
	ctx, b := fixture(t)
	ids := []string{}
	for _, status := range []string{"READY", "RETRY", "RUNNING", "DEAD", "DONE"} {
		m := message(d.ID())
		must(t, b.Enqueue(ctx, m))
		ids = append(ids, m.ID)
		_, e := b.cfg.DB.ExecContext(ctx, "UPDATE kafka_tasks SET state=?,updated_at=TIMESTAMPADD(HOUR,-48,UTC_TIMESTAMP(6)) WHERE task_id=?", status, m.ID)
		must(t, e)
	}
	n, e := b.Trim(ctx, 24*time.Hour)
	must(t, e)
	if n != 1 {
		t.Fatal("trim count", n)
	}
	for _, id := range ids[:4] {
		_ = state(t, ctx, b, id)
	}
}

func TestKafkaTaskDeliveryDuplicatePoisonAndFailedSettlementReplay(t *testing.T) {
	ctx, b := fixture(t)
	m := message(d.ID())
	must(t, b.Enqueue(ctx, m))
	// Duplicate publication does not duplicate execution after durable settlement.
	in := delivery(t, ctx, b, m)
	raw, _ := json.Marshal(in)
	for i := 0; i < 2; i++ {
		must(t, b.client.ProduceSync(ctx, &kgo.Record{Topic: b.Topic(m.Lane), Key: []byte(b.namespace + ":" + m.ID), Value: raw}).FirstErr())
	}
	must(t, b.client.ProduceSync(ctx, &kgo.Record{Topic: b.Topic(m.Lane), Value: []byte(`{"secret":"do-not-copy",invalid`)}).FirstErr())
	var calls atomic.Int32
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- b.Consume(run, m.Lane, 300*time.Millisecond, func(context.Context, Message) (Outcome, error) { calls.Add(1); return Outcome{State: "DONE"}, nil })
	}()
	wait(t, ctx, func() bool {
		var count int
		must(t, b.cfg.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM kafka_task_poison WHERE namespace_id=?", b.namespace).Scan(&count))
		return state(t, ctx, b, m.ID) == "DONE" && count == 1
	})
	cancel()
	must(t, <-done)
	if calls.Load() != 1 {
		t.Fatal("duplicate handler", calls.Load())
	}
	var poison string
	must(t, b.cfg.DB.QueryRowContext(ctx, "SELECT body FROM kafka_task_poison WHERE namespace_id=?", b.namespace).Scan(&poison))
	if strings.Contains(poison, "do-not-copy") {
		t.Fatal("poison secret copied")
	}
	// A handler error leaves offset uncommitted. The next session obtains an
	// expired SQL lease and sees the same record, then commits successful work.
	m = message(d.ID())
	must(t, b.Enqueue(ctx, m))
	_, e := b.DispatchOnce(ctx)
	must(t, e)
	calls.Store(0)
	run, cancel = context.WithCancel(ctx)
	done = make(chan error, 1)
	go func() {
		done <- b.Consume(run, m.Lane, 300*time.Millisecond, func(context.Context, Message) (Outcome, error) {
			if calls.Add(1) == 1 {
				return Outcome{}, errors.New("synthetic SQL failure")
			}
			return Outcome{State: "DONE"}, nil
		})
	}()
	wait(t, ctx, func() bool { return state(t, ctx, b, m.ID) == "DONE" })
	cancel()
	must(t, <-done)
	if calls.Load() != 2 {
		t.Fatal("failed settlement not replayed", calls.Load())
	}
}

func TestKafkaTaskBrokerOutageRetainsSQLHandoff(t *testing.T) {
	ctx, b := fixture(t)
	cfg := b.cfg
	cfg.Brokers = []string{"127.0.0.1:1"}
	offline, e := New(cfg)
	must(t, e)
	defer offline.Close()
	m := message(d.ID())
	must(t, offline.Enqueue(ctx, m))
	send, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if _, e = offline.DispatchOnce(send); e == nil {
		t.Fatal("offline broker send succeeded")
	}
	if state(t, ctx, b, m.ID) != "READY" {
		t.Fatal("outage stranded publication")
	}
	n, e := b.DispatchOnce(ctx)
	must(t, e)
	if n != 1 {
		t.Fatal("outage replay", n)
	}
}

func TestKafkaTaskKilledConsumerRecovery(t *testing.T) {
	if os.Getenv("CT_TASK_CHILD") == "1" {
		ctx := context.Background()
		db, e := sql.Open("mysql", os.Getenv("MYSQL_TEST_DSN"))
		must(t, e)
		defer db.Close()
		b, e := New(Config{DB: db, Brokers: strings.Split(os.Getenv("KAFKA_TEST_BROKERS"), ","), Prefix: os.Getenv("CT_TASK_PREFIX"), Namespace: os.Getenv("CT_TASK_NAMESPACE"), Lanes: []string{"ASSESS", "ANALYZE"}, Capacity: 6, OwnerCapacity: 3, Lease: time.Second})
		must(t, e)
		defer b.Close()
		_ = b.Consume(ctx, "ASSESS", 300*time.Millisecond, func(ctx context.Context, m Message) (Outcome, error) {
			_, e := db.ExecContext(ctx, "INSERT IGNORE INTO completed_tasks(id,completed_at) VALUES(?,UTC_TIMESTAMP(6))", m.ID)
			if e != nil {
				return Outcome{}, e
			}
			<-ctx.Done()
			return Outcome{}, ctx.Err()
		})
		return
	}
	ctx, b := fixture(t)
	m := message(d.ID())
	must(t, b.Enqueue(ctx, m))
	_, e := b.DispatchOnce(ctx)
	must(t, e)
	var dsn string // sqlfixture returns this test's existing isolated schema.
	dsn = sqlfixture.Database(t, os.Getenv("MYSQL_TEST_DSN"), "kafka_tasks")
	cmd := exec.Command(os.Args[0], "-test.run=^TestKafkaTaskKilledConsumerRecovery$")
	cmd.Env = append(os.Environ(), "CT_TASK_CHILD=1", "CT_TASK_NAMESPACE="+b.cfg.Namespace, "CT_TASK_PREFIX="+b.cfg.Prefix, "MYSQL_TEST_DSN="+dsn)
	must(t, cmd.Start())
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	wait(t, ctx, func() bool {
		var n int
		must(t, b.cfg.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM completed_tasks WHERE id=?", m.ID).Scan(&n))
		return n == 1
	})
	must(t, cmd.Process.Kill())
	_ = cmd.Wait()
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	var calls atomic.Int32
	go func() {
		done <- b.Consume(run, m.Lane, 300*time.Millisecond, func(ctx context.Context, m Message) (Outcome, error) {
			calls.Add(1)
			_, e := b.cfg.DB.ExecContext(ctx, "INSERT IGNORE INTO completed_tasks(id,completed_at) VALUES(?,UTC_TIMESTAMP(6))", m.ID)
			return Outcome{State: "DONE"}, e
		})
	}()
	wait(t, ctx, func() bool { return state(t, ctx, b, m.ID) == "DONE" })
	cancel()
	must(t, <-done)
	var n int
	must(t, b.cfg.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM completed_tasks WHERE id=?", m.ID).Scan(&n))
	if n != 1 || calls.Load() != 1 {
		t.Fatal("kill replay not idempotent", n, calls.Load())
	}
}

func TestJournalRecoveryCopyRotatesEpochAndReplaysOnlyUnfinished(t *testing.T) {
	ctx, original := fixture(t)
	dsn := sqlfixture.RecoveryDatabase(t, os.Getenv("MYSQL_TEST_DSN"), "kafka_tasks")
	restored, e := p.Open(ctx, dsn)
	must(t, e)
	defer restored.DB.Close()
	must(t, restored.Migrate(ctx))
	cfg := original.cfg
	cfg.DB = restored.DB
	b, e := New(cfg)
	must(t, e)
	defer b.Close()
	must(t, b.Init(ctx))
	oldEpoch, e := original.epoch(ctx)
	must(t, e)
	_, e = b.cfg.DB.ExecContext(ctx, "UPDATE task_queue_namespaces SET epoch=? WHERE namespace_id=?", oldEpoch, b.namespace)
	must(t, e)
	active := message(d.ID())
	completed := message(d.ID())
	must(t, b.Enqueue(ctx, active))
	must(t, b.Enqueue(ctx, completed))
	in := delivery(t, ctx, b, active)
	_, _, e = b.claim(ctx, in)
	must(t, e)
	receipt, _, e := b.claim(ctx, delivery(t, ctx, b, completed))
	must(t, e)
	must(t, b.finish(ctx, receipt, Outcome{State: "DONE"}))
	_, e = restored.PrepareRecovery(ctx)
	must(t, e)
	newEpoch, e := b.epoch(ctx)
	must(t, e)
	if newEpoch == oldEpoch {
		t.Fatal("task epoch not rotated")
	}
	if state(t, ctx, b, active.ID) != "READY" || state(t, ctx, b, completed.ID) != "DONE" {
		t.Fatal("restore lost terminal fence")
	}
	if _, done, e := b.claim(ctx, in); e != nil || !done {
		t.Fatal("retained pre-restore delivery accepted", done, e)
	}
	must(t, b.SetLease(10*time.Second))
	current := delivery(t, ctx, b, active)
	activeReceipt, done, e := b.claim(ctx, current)
	must(t, e)
	if done {
		t.Fatal("restored task could not resume")
	}
	_, e = restored.PrepareRecovery(ctx)
	must(t, e)
	repeated, e := b.epoch(ctx)
	must(t, e)
	if repeated != newEpoch || state(t, ctx, b, active.ID) != "RUNNING" {
		t.Fatal("repeated prepare reset active task transport")
	}
	must(t, b.finish(ctx, activeReceipt, Outcome{State: "DONE"}))

}

func TestKafkaTaskSQLSettlementFailureDoesNotCommitOffset(t *testing.T) {
	ctx, b := fixture(t)
	m := message(d.ID())
	must(t, b.Enqueue(ctx, m))
	_, e := b.DispatchOnce(ctx)
	must(t, e)
	_, e = b.cfg.DB.ExecContext(ctx, `ALTER TABLE kafka_tasks ADD CONSTRAINT synthetic_no_done CHECK (state<>'DONE')`)
	must(t, e)
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	var calls atomic.Int32
	go func() {
		done <- b.Consume(run, m.Lane, 300*time.Millisecond, func(ctx context.Context, m Message) (Outcome, error) {
			calls.Add(1)
			_, e := b.cfg.DB.ExecContext(ctx, "INSERT IGNORE INTO completed_tasks(id,completed_at) VALUES(?,UTC_TIMESTAMP(6))", m.ID)
			return Outcome{State: "DONE"}, e
		})
	}()
	defer func() { cancel(); must(t, <-done) }()
	wait(t, ctx, func() bool {
		r := httptest.NewRecorder()
		b.cfg.Metrics.ServeHTTP(r, httptest.NewRequest("GET", "/metrics", nil))
		var values map[string]float64
		must(t, json.Unmarshal(r.Body.Bytes(), &values))
		return values["task_settlement_errors_total"] > 0
	})
	if state(t, ctx, b, m.ID) != "RUNNING" {
		t.Fatal("failed SQL settlement changed state")
	}
	_, e = b.cfg.DB.ExecContext(ctx, "ALTER TABLE kafka_tasks DROP CHECK synthetic_no_done")
	must(t, e)
	wait(t, ctx, func() bool { return state(t, ctx, b, m.ID) == "DONE" })
	var n int
	must(t, b.cfg.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM completed_tasks WHERE id=?", m.ID).Scan(&n))
	if n != 1 || calls.Load() != 2 {
		t.Fatal("business commit replay", n, calls.Load())
	}
}

package pipeline

import (
	"context"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/redis/go-redis/v9"
	"testing"
	"time"
)

func typedFixture(t *testing.T) (context.Context, *Queue) {
	ctx, q := repairQueue(t)
	q.Partitioned = true
	q.Capacity = 4
	q.OwnerCapacity = 3
	if e := q.Init(ctx); e != nil {
		t.Fatal(e)
	}
	return ctx, q
}
func ownedTask(owner, typ string) p.Task {
	v := p.NewTask(typ, d.ID())
	v.OwnerID = owner
	if typ == "SOURCE_IMPORT" {
		v.Generation = 1
		v.ID = p.SourceImportTaskID(v.EntityID, 1)
	}
	return v
}
func readLane(t *testing.T, ctx context.Context, q *Queue, consumer string) redis.XMessage {
	t.Helper()
	xs, e := q.R.XReadGroup(ctx, &redis.XReadGroupArgs{Group: q.Group(), Consumer: consumer, Streams: []string{q.Stream(), ">"}, Count: 1}).Result()
	if e != nil || len(xs) != 1 || len(xs[0].Messages) != 1 {
		t.Fatalf("read: %v %v", xs, e)
	}
	return xs[0].Messages[0]
}
func TestTypedFairBoundDedupAndIsolation(t *testing.T) {
	ctx, q := typedFixture(t)
	a, b := d.ID(), d.ID()
	tasks := []p.Task{ownedTask(a, "ASSESS"), ownedTask(a, "ASSESS"), ownedTask(a, "ASSESS"), ownedTask(b, "ASSESS")}
	for _, v := range tasks {
		if e := q.Publish(ctx, v); e != nil {
			t.Fatal(e)
		}
	}
	if e := q.Publish(ctx, tasks[0]); e != nil {
		t.Fatal("duplicate consumed capacity", e)
	}
	if e := q.Publish(ctx, ownedTask(a, "ASSESS")); !errors.Is(e, ErrQueueFull) {
		t.Fatal(e)
	}
	lane := q.ForLane("ASSESS")
	expected := []string{tasks[0].ID, tasks[3].ID, tasks[1].ID, tasks[2].ID}
	for _, id := range expected {
		if n, e := lane.Pump(ctx); n != 1 || e != nil {
			t.Fatal(n, e)
		}
		m := readLane(t, ctx, lane, "one")
		v, e := Decode(m)
		if e != nil || v.ID != id {
			t.Fatal(v, e, id)
		}
		if e = lane.Ack(ctx, m.ID); e != nil {
			t.Fatal(e)
		}
		if e = lane.Ack(ctx, m.ID); e != nil {
			t.Fatal(e)
		}
	}
	stats, e := q.Stats(ctx)
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range stats {
		if s.Outstanding != 0 || s.InFlight != 0 {
			t.Fatal(s)
		}
	}
	if e = q.ForLane("ANALYZE").Publish(ctx, tasks[0]); !errors.Is(e, ErrWrongLane) {
		t.Fatal(e)
	}
	// A valid alien envelope must remain pending, not be consumed by this worker.
	id, e := q.R.XAdd(ctx, &redis.XAddArgs{Stream: lane.Stream(), Values: map[string]any{"task": d.JSON(ownedTask(a, "ANALYZE"))}}).Result()
	if e != nil {
		t.Fatal(e)
	}
	m := readLane(t, ctx, lane, "wrong")
	w := &Worker{Queue: lane, Metrics: observability.New(), Timeout: time.Second}
	w.handle(ctx, m)
	if n := q.R.XPending(ctx, lane.Stream(), lane.Group()).Val().Count; n != 1 {
		t.Fatal("wrong ACK", id, n)
	}
}
func TestTypedLegacyPendingFullRecoveryAndRetry(t *testing.T) {
	ctx, q := typedFixture(t)
	owner := d.ID()
	q.ResolveOwner = func(context.Context, p.Task) (string, error) { return owner, nil }
	legacy := &Queue{R: q.R, Prefix: q.Prefix}
	task := ownedTask("", "SOURCE_IMPORT")
	if e := legacy.Publish(ctx, task); e != nil {
		t.Fatal(e)
	}
	m := readLane(t, ctx, legacy, "old-worker")
	for i := 0; i < 3; i++ {
		if e := q.Publish(ctx, ownedTask(owner, "SOURCE_IMPORT")); e != nil {
			t.Fatal(e)
		}
	}
	if moved, e := q.BridgeLegacy(ctx, m, "old-worker"); e != nil || moved {
		t.Fatal(moved, e)
	}
	if n := q.R.XPending(ctx, legacy.Stream(), legacy.Group()).Val().Count; n != 1 {
		t.Fatal(n)
	}
	lane := q.ForLane("SOURCE_IMPORT")
	if _, e := lane.Pump(ctx); e != nil {
		t.Fatal(e)
	}
	first := readLane(t, ctx, lane, "new-worker")
	if e := lane.Ack(ctx, first.ID); e != nil {
		t.Fatal(e)
	}
	time.Sleep(2 * time.Millisecond)
	recovered, e := legacy.Recover(ctx, "restart", time.Millisecond)
	if e != nil || len(recovered) != 1 {
		t.Fatal(recovered, e)
	}
	if moved, e := q.BridgeLegacy(ctx, recovered[0], "restart"); e != nil || !moved {
		t.Fatal(moved, e)
	}
	if moved, e := q.BridgeLegacy(ctx, recovered[0], "restart"); e != nil || !moved {
		t.Fatal("repeat bridge", moved, e)
	}
	for i := 0; i < 3; i++ {
		lane.Pump(ctx)
		msg := readLane(t, ctx, lane, "new-worker")
		v, _ := Decode(msg)
		if v.ID == task.ID {
			retry, e := lane.Fail(ctx, msg.ID, v, errors.New("429 rejected"), 4)
			if !retry || e != nil {
				t.Fatal(retry, e)
			}
			// Delayed retry survives its old format and receives the same fair routing.
			key := q.Prefix + "retry:SOURCE_IMPORT"
			raw := q.R.ZRange(ctx, key, 0, -1).Val()
			if len(raw) != 1 {
				t.Fatal(raw)
			}
			q.R.ZAdd(ctx, key, redis.Z{Score: 0, Member: raw[0]})
			n, e := q.Schedule(ctx)
			if n != 1 || e != nil {
				t.Fatal(n, e)
			}
		} else {
			if e := lane.Ack(ctx, msg.ID); e != nil {
				t.Fatal(e)
			}
		}
	}
	lane.Pump(ctx)
	msg := readLane(t, ctx, lane, "new-worker")
	v, _ := Decode(msg)
	if v.ID != task.ID || v.Attempt != 2 {
		t.Fatal(v)
	}
	retry, e := lane.Fail(ctx, msg.ID, v, ErrQueueExpired, 4)
	if retry || e != nil {
		t.Fatal(retry, e)
	}
	if n := q.R.HLen(ctx, q.Prefix+"dlq").Val(); n != 1 {
		t.Fatal(n)
	}
	if e := q.Redrive(ctx, v.ID); e != nil {
		t.Fatal(e)
	}
	if e := legacy.Init(ctx); e == nil {
		t.Fatal("unsafe legacy rollback accepted")
	}
	lane.Pump(ctx)
	msg = readLane(t, ctx, lane, "new-worker")
	v, _ = Decode(msg)
	if v.Attempt != 1 || v.QueueUntil.IsZero() {
		t.Fatal(v)
	}
	if e = lane.Ack(ctx, msg.ID); e != nil {
		t.Fatal(e)
	}
	if e = legacy.Init(ctx); e != nil {
		t.Fatal("drained rollback", e)
	}
}
func TestTypedRestartAndFailureKeyIsolation(t *testing.T) {
	ctx, q := typedFixture(t)
	a := d.ID()
	for _, name := range []string{"ANALYZE", "ASSESS"} {
		lane := q.ForLane(name)
		v := ownedTask(a, name)
		if e := q.Publish(ctx, v); e != nil {
			t.Fatal(e)
		}
		// Equal Redis message IDs in different streams must not share failure marks.
		keys := lane.queueKeys()
		q.R.HSet(ctx, keys[5], v.ID, "123-0")
		q.R.XAdd(ctx, &redis.XAddArgs{Stream: lane.Stream(), ID: "123-0", Values: map[string]any{"task": d.JSON(v)}})
		m := readLane(t, ctx, lane, "dead-worker")
		again := q.ForLane(name)
		time.Sleep(2 * time.Millisecond)
		recovered, e := again.Recover(ctx, "restarted", time.Millisecond)
		if e != nil || len(recovered) != 1 {
			t.Fatal(recovered, e)
		}
		retry, e := again.Fail(ctx, m.ID, v, errors.New("schema invalid"), 4)
		if retry || e != nil {
			t.Fatal(retry, e)
		}
		if _, e = again.Fail(ctx, m.ID, v, errors.New("schema invalid"), 4); e != nil {
			t.Fatal(e)
		}
	}
	if n := q.R.HLen(ctx, q.Prefix+"dlq").Val(); n != 2 {
		t.Fatal("cross-lane marker collision", n)
	}
}

func TestTypedRetryBackpressureRetainsPending(t *testing.T) {
	ctx, q := typedFixture(t)
	q.Capacity = 1
	q.OwnerCapacity = 1
	lane := q.ForLane("ASSESS")
	v := ownedTask(d.ID(), "ASSESS")
	if e := q.Publish(ctx, v); e != nil {
		t.Fatal(e)
	}
	lane.Pump(ctx)
	msg := readLane(t, ctx, lane, "worker")
	q.R.ZAdd(ctx, q.Prefix+"retry:ASSESS", redis.Z{Score: float64(time.Now().Add(time.Hour).UnixMilli()), Member: d.JSON(ownedTask(d.ID(), "ASSESS"))})
	if _, e := lane.Fail(ctx, msg.ID, v, errors.New("429"), 4); e == nil {
		t.Fatal("retry capacity ignored")
	}
	if n := q.R.XPending(ctx, lane.Stream(), lane.Group()).Val().Count; n != 1 {
		t.Fatal("full retry destination ACKed source", n)
	}
	if n := q.R.HLen(ctx, lane.queueKeys()[0]).Val(); n != 1 {
		t.Fatal("capacity reservation lost", n)
	}
	q.R.Del(ctx, q.Prefix+"retry:ASSESS")
	if _, e := lane.Fail(ctx, msg.ID, v, errors.New("429"), 4); e != nil {
		t.Fatal(e)
	}
	if _, e := lane.Fail(ctx, msg.ID, v, errors.New("429"), 4); e != nil {
		t.Fatal("idempotent full-destination replay", e)
	}
}

func TestTypedExpiredBeforeExecutionNeverCallsProcessor(t *testing.T) {
	ctx, q := typedFixture(t)
	v := ownedTask(d.ID(), "ASSESS")
	v.QueueUntil = time.Now().Add(-time.Second)
	if e := q.Publish(ctx, v); e != nil {
		t.Fatal(e)
	}
	lane := q.ForLane("ASSESS")
	lane.Pump(ctx)
	msg := readLane(t, ctx, lane, "worker")
	// Nil Store is intentional: an expired task cannot enter Process at all.
	w := &Worker{Queue: lane, Timeout: time.Second, MaxAttempts: 4, Metrics: observability.New()}
	w.handle(ctx, msg)
	if n := q.R.HLen(ctx, q.Prefix+"dlq").Val(); n != 1 {
		t.Fatal("expiry did not terminate", n)
	}
	if n := q.R.HLen(ctx, lane.queueKeys()[0]).Val(); n != 0 {
		t.Fatal("expiry leaked capacity", n)
	}
}

func TestTypedMalformedLegacyRetryIsQuarantinedWithoutBlockingValidWork(t *testing.T) {
	ctx, q := typedFixture(t)
	v := ownedTask(d.ID(), "ASSESS")
	key := q.Prefix + "retry:ASSESS"
	q.R.ZAdd(ctx, key, redis.Z{Score: 0, Member: "{broken"}, redis.Z{Score: 0, Member: d.JSON(v)})
	n, e := q.Schedule(ctx)
	if e != nil || n != 1 {
		t.Fatal(n, e)
	}
	if n := q.R.HLen(ctx, q.Prefix+"poison:retry").Val(); n != 1 {
		t.Fatal(n)
	}
	if n := q.R.ZCard(ctx, key).Val(); n != 0 {
		t.Fatal(n)
	}
}

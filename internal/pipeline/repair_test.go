package pipeline

import (
	"context"
	"fmt"
	"github.com/KanaDoodle/CampusTrace/internal/config"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/redis/go-redis/v9"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRepairBackoffBounds(t *testing.T) {
	for _, n := range []int{-1, -100, 0, 1, 100, int(^uint(0) >> 1)} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			defer func() {
				if v := recover(); v != nil {
					t.Errorf("F04 panic: %v", v)
				}
			}()
			if b := Backoff(n); b < time.Second || b > 1536*time.Second {
				t.Error(b)
			}
		})
	}
}
func repairQueue(t *testing.T) (context.Context, *Queue) {
	t.Helper()
	if os.Getenv("CAMPUS_INTEGRATION") != "1" {
		t.Skip("requires Redis")
	}
	ctx := context.Background()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:16379"
	}
	r := redis.NewClient(&redis.Options{Addr: addr})
	q := &Queue{R: r, Prefix: "repair:" + d.ID() + ":"}
	if e := q.Init(ctx); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		keys, _ := r.Keys(ctx, q.Prefix+"*").Result()
		if len(keys) > 0 {
			r.Del(ctx, keys...)
		}
		r.Close()
	})
	return ctx, q
}
func TestRepairPoisonIsolation(t *testing.T) {
	ctx, q := repairQueue(t)
	cfg := config.Load()
	store, e := p.Open(ctx, config.Env("MYSQL_TEST_DSN", strings.Replace(cfg.DSN, "/campustrace?", "/campustrace_test?", 1)))
	if e != nil {
		t.Fatal(e)
	}
	defer store.DB.Close()
	base := p.NewTask("ANALYZE", "no-entity")
	tasks := []p.Task{base, base, base, base, base, base, base}
	tasks[0].Attempt = 0
	tasks[1].Attempt = -3
	tasks[2].Type = "ALIEN"
	tasks[3].ID = ""
	tasks[4].Version = 99
	tasks[5].EntityID = ""
	tasks[6].Attempt = 1000000
	raw := []string{"{broken", strings.Repeat("x", 65537)}
	for _, v := range tasks {
		raw = append(raw, d.JSON(v))
	}
	for i, v := range raw {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			defer func() {
				if x := recover(); x != nil {
					t.Errorf("F04: worker panic %v", x)
				}
			}()
			id, e := q.R.XAdd(ctx, &redis.XAddArgs{Stream: q.Stream(), Values: map[string]any{"task": v}}).Result()
			if e != nil {
				t.Fatal(e)
			}
			q.R.XReadGroup(ctx, &redis.XReadGroupArgs{Group: q.Group(), Consumer: "poison-test", Streams: []string{q.Stream(), ">"}, Count: 1})
			w := &Worker{Store: store, Queue: q, Timeout: time.Second, MaxAttempts: 2, Metrics: observability.New()}
			w.handle(ctx, redis.XMessage{ID: id, Values: map[string]any{"task": v}})
			pending, e := q.R.XPending(ctx, q.Stream(), q.Group()).Result()
			if e != nil || pending.Count != 0 {
				t.Errorf("F04: poison not safely acknowledged: %v %v", pending, e)
			}
			if n := q.R.HLen(ctx, q.Prefix+"poison").Val(); n != int64(i+1) {
				t.Errorf("F04: quarantine count %d", n)
			}
		})
	}
}
func TestRepairFailureReentrancy(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(fmt.Sprint(retry), func(t *testing.T) {
			ctx, q := repairQueue(t)
			task := p.NewTask("ANALYZE", "x")
			if e := q.Publish(ctx, task); e != nil {
				t.Fatal(e)
			}
			xs, e := q.R.XReadGroup(ctx, &redis.XReadGroupArgs{Group: q.Group(), Consumer: "fault-test", Streams: []string{q.Stream(), ">"}, Count: 1}).Result()
			if e != nil {
				t.Fatal(e)
			}
			id := xs[0].Messages[0].ID
			key := q.Prefix + "dlq"
			failure := fmt.Errorf("schema error")
			if retry {
				key = q.Prefix + "retry:ANALYZE"
				failure = fmt.Errorf("connection timeout")
			}
			q.R.Set(ctx, key, "WRONGTYPE", 0)
			if _, e = q.Fail(ctx, id, task, failure, 3); e == nil {
				t.Fatal("expected WRONGTYPE")
			}
			q.R.Del(ctx, key)
			for i := 0; i < 2; i++ {
				if _, e = q.Fail(ctx, id, task, failure, 3); e != nil {
					t.Fatal(e)
				}
			}
			n := q.R.HLen(ctx, key).Val()
			if retry {
				n = q.R.ZCard(ctx, key).Val()
			}
			if n != 1 {
				t.Fatalf("F07: transfer lost or duplicated: %d", n)
			}
			if q.R.XPending(ctx, q.Stream(), q.Group()).Val().Count != 0 {
				t.Fatal("not acknowledged")
			}
		})
	}
}

func TestRepairPoisonRedisErrorIsRecoverable(t *testing.T) {
	ctx, q := repairQueue(t)
	id, e := q.R.XAdd(ctx, &redis.XAddArgs{Stream: q.Stream(), Values: map[string]any{"task": "{bad"}}).Result()
	if e != nil {
		t.Fatal(e)
	}
	q.R.XReadGroup(ctx, &redis.XReadGroupArgs{Group: q.Group(), Consumer: "quarantine-fault", Streams: []string{q.Stream(), ">"}, Count: 1})
	w := &Worker{Queue: q, Timeout: time.Second, Metrics: observability.New()}
	m := redis.XMessage{ID: id, Values: map[string]any{"task": "{bad"}}
	q.R.Set(ctx, q.Prefix+"poison", "WRONGTYPE", 0)
	w.handle(ctx, m, "quarantine-fault")
	if q.R.XPending(ctx, q.Stream(), q.Group()).Val().Count != 1 {
		t.Fatal("failed quarantine acknowledged poison")
	}
	q.R.Del(ctx, q.Prefix+"poison")
	w.handle(ctx, m, "replacement")
	if q.R.XPending(ctx, q.Stream(), q.Group()).Val().Count != 0 || q.R.HLen(ctx, q.Prefix+"poison").Val() != 1 {
		t.Fatal("quarantine not recoverable")
	}
}

func TestRepairFailureAfterDestinationWrite(t *testing.T) {
	ctx, q := repairQueue(t)
	task := p.NewTask("ANALYZE", "x")
	if e := q.Publish(ctx, task); e != nil {
		t.Fatal(e)
	}
	xs, e := q.R.XReadGroup(ctx, &redis.XReadGroupArgs{Group: q.Group(), Consumer: "partial", Streams: []string{q.Stream(), ">"}, Count: 1}).Result()
	if e != nil {
		t.Fatal(e)
	}
	id := xs[0].Messages[0].ID
	ms, e := strconv.ParseInt(strings.SplitN(id, "-", 2)[0], 10, 64)
	if e != nil {
		t.Fatal(e)
	}
	transferred := task
	transferred.Attempt++
	transferred.FirstFailed = time.UnixMilli(ms).UTC()
	fault := q.Prefix + "wrong"
	q.R.Set(ctx, fault, "WRONGTYPE", 0)
	// Actual Redis runtime error AFTER ZADD demonstrates a non-rolled-back target
	// write. The production transition must rerun without a duplicate transfer.
	script := redis.NewScript(`redis.call('ZADD',KEYS[1],0,ARGV[1]);redis.call('HSET',KEYS[2],'x','y')`)
	if e = script.Run(ctx, q.R, []string{q.Prefix + "retry:ANALYZE", fault}, d.JSON(transferred)).Err(); e == nil {
		t.Fatal("expected partial script error")
	}
	if q.R.ZCard(ctx, q.Prefix+"retry:ANALYZE").Val() != 1 {
		t.Fatal("fault did not leave partial write")
	}
	q.R.SAdd(ctx, q.Prefix+"failed-messages", id) // Legacy premature marker is never trusted.
	for i := 0; i < 2; i++ {
		if _, e = q.Fail(ctx, id, task, fmt.Errorf("connection timeout"), 3); e != nil {
			t.Fatal(e)
		}
	}
	if q.R.ZCard(ctx, q.Prefix+"retry:ANALYZE").Val() != 1 {
		t.Fatal("partial transition rerun duplicated transfer")
	}
	if q.R.XPending(ctx, q.Stream(), q.Group()).Val().Count != 0 {
		t.Fatal("transfer not acknowledged")
	}
}

package pipeline

import (
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestDispatchBackoffIsBoundedAndWorkResetsIt(t *testing.T) {
	delay := 200 * time.Millisecond
	for range 20 {
		next := dispatchDelay(delay, 0, false)
		if next < delay || next > 3*time.Second {
			t.Fatal(delay, next)
		}
		delay = next
	}
	if delay != 3*time.Second || dispatchDelay(delay, 1, false) != 200*time.Millisecond || dispatchDelay(200*time.Millisecond, 1, true) != 3*time.Second {
		t.Fatal("dispatch pacing lost responsiveness or failure backoff")
	}
}

func TestHistoryProtectsAllGroupsUnreadAndPending(t *testing.T) {
	ctx, q := repairQueue(t)
	old := time.Now().Add(-48 * time.Hour).UnixMilli()
	ids := []string{}
	for i := range 300 {
		id, err := q.R.XAdd(ctx, &redis.XAddArgs{Stream: q.Stream(), ID: fmt.Sprintf("%d-0", old+int64(i)), Values: map[string]any{"task": "history-fixture"}}).Result()
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	recent, err := q.R.XAdd(ctx, &redis.XAddArgs{Stream: q.Stream(), Values: map[string]any{"task": "recent-fixture"}}).Result()
	if err != nil {
		t.Fatal(err)
	}
	read := func(group string, count int64) []string {
		t.Helper()
		xs, err := q.R.XReadGroup(ctx, &redis.XReadGroupArgs{Group: group, Consumer: "history-test", Streams: []string{q.Stream(), ">"}, Count: count}).Result()
		if err != nil {
			t.Fatal(err)
		}
		var values []string
		for _, stream := range xs {
			for _, message := range stream.Messages {
				values = append(values, message.ID)
			}
		}
		return values
	}
	trim := func() int64 {
		t.Helper()
		n, err := q.TrimAcknowledged(ctx, 24*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	workers := read(q.Group(), 250)
	ack := append(append([]string{}, workers[:120]...), workers[121:]...)
	if err = q.R.XAck(ctx, q.Stream(), q.Group(), ack...).Err(); err != nil {
		t.Fatal(err)
	}
	if err = q.R.XGroupCreate(ctx, q.Stream(), "second-group", "0").Err(); err != nil {
		t.Fatal(err)
	}
	if n := trim(); n != 0 {
		t.Fatal("trimmed a group's wholly unread stream", n)
	}
	second := read("second-group", 210)
	if err = q.R.XAck(ctx, q.Stream(), "second-group", second...).Err(); err != nil {
		t.Fatal(err)
	}
	q.R.HSet(ctx, q.Prefix+"dlq", "failure", "kept")
	q.R.ZAdd(ctx, q.Prefix+"retry:ANALYZE", redis.Z{Score: 1, Member: "kept"})
	if n := trim(); n == 0 || n > 120 {
		t.Fatal("did not trim only history before oldest pending", n)
	}
	assertPresent := func(id string) {
		t.Helper()
		xs, err := q.R.XRangeN(ctx, q.Stream(), id, id, 1).Result()
		if err != nil || len(xs) != 1 {
			t.Fatal("protected message removed", id, err)
		}
	}
	assertPresent(ids[120]) // pending
	assertPresent(ids[210]) // unread by second group
	assertPresent(ids[250]) // unread by workers
	assertPresent(recent)
	if pending := q.R.XPending(ctx, q.Stream(), q.Group()).Val(); pending.Count != 1 || pending.Lower != ids[120] {
		t.Fatal("pending state changed", pending)
	}
	q.R.XAck(ctx, q.Stream(), q.Group(), ids[120])
	trim()
	assertPresent(ids[210])
	for _, group := range []string{q.Group(), "second-group"} {
		remaining := read(group, 500)
		q.R.XAck(ctx, q.Stream(), group, remaining...)
	}
	trim()
	assertPresent(recent)
	if q.R.HGet(ctx, q.Prefix+"dlq", "failure").Val() != "kept" || q.R.ZCard(ctx, q.Prefix+"retry:ANALYZE").Val() != 1 {
		t.Fatal("history cleanup altered failed or delayed work")
	}
}

func TestHistoryNeverTrimsWithoutGroupsOrPastFirstPending(t *testing.T) {
	ctx, q := repairQueue(t)
	for i := range 220 {
		q.R.XAdd(ctx, &redis.XAddArgs{Stream: q.Stream(), ID: fmt.Sprintf("%d-0", time.Now().Add(-48*time.Hour).UnixMilli()+int64(i)), Values: map[string]any{"task": "fixture"}})
	}
	xs, err := q.R.XReadGroup(ctx, &redis.XReadGroupArgs{Group: q.Group(), Consumer: "first-pending", Streams: []string{q.Stream(), ">"}, Count: 220}).Result()
	if err != nil {
		t.Fatal(err)
	}
	var ack []string
	for _, message := range xs[0].Messages[1:] {
		ack = append(ack, message.ID)
	}
	q.R.XAck(ctx, q.Stream(), q.Group(), ack...)
	if n, err := q.TrimAcknowledged(ctx, time.Hour); err != nil || n != 0 {
		t.Fatal("removed earliest unacknowledged work", n, err)
	}
	q.R.XGroupDestroy(ctx, q.Stream(), q.Group())
	if n, err := q.TrimAcknowledged(ctx, time.Hour); err != nil || n != 0 {
		t.Fatal("trimmed unknown delivery state", n, err)
	}
	if _, err := q.TrimAcknowledged(ctx, time.Minute); err == nil {
		t.Fatal("accepted unsafe retention")
	}
	q.R.Del(ctx, q.Stream())
	q.R.Set(ctx, q.Stream(), "wrong", 0)
	if _, err := q.TrimAcknowledged(ctx, time.Hour); err == nil {
		t.Fatal("ignored invalid stream type")
	}
}

func TestSourceImportRetryIsScheduled(t *testing.T) {
	ctx, q := repairQueue(t)
	q.R.ZAdd(ctx, q.Prefix+"retry:SOURCE_IMPORT", redis.Z{Score: 0, Member: "source-import-fixture"})
	n, err := q.Schedule(ctx)
	if err != nil || n != 1 || q.R.ZCard(ctx, q.Prefix+"retry:SOURCE_IMPORT").Val() != 0 || q.R.XLen(ctx, q.Stream()).Val() != 1 {
		t.Fatal("source import retry not handed off", n, err)
	}
}

func TestHistoryCleanupIsBoundedAndPreservesRecentMessages(t *testing.T) {
	ctx, q := repairQueue(t)
	old := time.Now().Add(-48 * time.Hour).UnixMilli()
	pipe := q.R.Pipeline()
	for i := range 12050 {
		pipe.XAdd(ctx, &redis.XAddArgs{Stream: q.Stream(), ID: fmt.Sprintf("%d-0", old+int64(i)), Values: map[string]any{"task": "bounded-fixture"}})
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	recent, err := q.R.XAdd(ctx, &redis.XAddArgs{Stream: q.Stream(), Values: map[string]any{"task": "recent"}}).Result()
	if err != nil {
		t.Fatal(err)
	}
	xs, err := q.R.XReadGroup(ctx, &redis.XReadGroupArgs{Group: q.Group(), Consumer: "bounded", Streams: []string{q.Stream(), ">"}, Count: 13000}).Result()
	if err != nil || len(xs) != 1 || len(xs[0].Messages) != 12051 {
		t.Fatal("fixture not consumed", err)
	}
	var ack []string
	for _, message := range xs[0].Messages {
		ack = append(ack, message.ID)
	}
	if err = q.R.XAck(ctx, q.Stream(), q.Group(), ack...).Err(); err != nil {
		t.Fatal(err)
	}
	n, err := q.TrimAcknowledged(ctx, 24*time.Hour)
	if err != nil || n < 1 || n > 10000 {
		t.Fatal("unbounded cleanup", n, err)
	}
	for range 3 {
		if _, err = q.TrimAcknowledged(ctx, 24*time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	if remaining, err := q.R.XRangeN(ctx, q.Stream(), recent, recent, 1).Result(); err != nil || len(remaining) != 1 {
		t.Fatal("recent acknowledged task removed", err)
	}
	if remaining := q.R.XLen(ctx, q.Stream()).Val(); remaining < 1 || remaining > 101 {
		t.Fatal("old history did not converge within node granularity", remaining)
	}
}

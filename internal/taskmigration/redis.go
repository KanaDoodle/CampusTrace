// Package taskmigration contains the one-time Redis-to-Kafka upgrade reader.
// Normal task producers and workers never use Redis Streams.
package taskmigration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/pipeline"
	"github.com/KanaDoodle/CampusTrace/internal/taskqueue"
	"github.com/redis/go-redis/v9"
)

type Report struct {
	Tasks, Poison   int
	AlreadyImported bool
}

// Run is read-only against Redis. All old producers/consumers must be stopped;
// old keys remain available for backup and inspection after SQL handoff.
func Run(ctx context.Context, q *pipeline.Queue) (report Report, err error) {
	if err = q.Broker.Init(ctx); err != nil {
		return
	}
	if report.AlreadyImported, err = q.Broker.LegacyImported(ctx); err != nil || report.AlreadyImported {
		return
	}
	transfer := func(origin, raw, state string, due time.Time) error {
		var task p.Task
		var decodeErr error
		category := "MIGRATED"
		if state == "DEAD" {
			var failure pipeline.Failure
			decodeErr = d.Strict([]byte(raw), &failure)
			if decodeErr == nil {
				task = failure.Task
				if task.FirstFailed.IsZero() {
					task.FirstFailed = failure.FirstFailed
				}
				if len(failure.Category) <= 32 {
					category = failure.Category
				}
			} else {
				task = p.Task{}
			}
		} else {
			task, decodeErr = pipeline.Decode([]byte(raw))
		}
		if e := pipeline.ValidateTask(task); e != nil || decodeErr != nil {
			report.Poison++
			return q.Broker.ImportPoison(ctx, origin, raw)
		}
		if task.OwnerID == "" && q.ResolveOwner != nil {
			var e error
			task.OwnerID, e = q.ResolveOwner(ctx, task)
			if e != nil {
				return e
			}
		}
		lane := task.Type
		if task.OwnerID == "" {
			lane = "SYSTEM_" + lane
		}
		e := q.Broker.ImportLegacy(ctx, taskqueue.Message{ID: task.ID, Lane: lane, Owner: task.OwnerID, Body: json.RawMessage(d.JSON(task))}, state, due, category)
		if e == nil {
			report.Tasks++
		}
		return e
	}
	// Transfer terminal and delayed work first. Stale transport duplicates must
	// never replace a newer attempt or turn an existing dead letter into READY.
	scanHash := func(key, state string) error {
		var cursor uint64
		for {
			values, next, e := q.R.HScan(ctx, key, cursor, "*", 100).Result()
			if e != nil {
				return e
			}
			for i := 0; i < len(values); i += 2 {
				if e = transfer(key+":"+values[i], values[i+1], state, time.Time{}); e != nil {
					return e
				}
			}
			cursor = next
			if cursor == 0 {
				return nil
			}
		}
	}
	if err = scanHash(q.Prefix+"dlq", "DEAD"); err != nil {
		return
	}
	for _, kind := range pipeline.TaskTypes {
		key := q.Prefix + "retry:" + kind
		for offset := int64(0); ; offset += 100 {
			entries, e := q.R.ZRangeWithScores(ctx, key, offset, offset+99).Result()
			if e != nil {
				err = e
				return
			}
			for _, v := range entries {
				if err = transfer(key, fmt.Sprint(v.Member), "RETRY", time.UnixMilli(int64(v.Score))); err != nil {
					return
				}
			}
			if len(entries) < 100 {
				break
			}
		}
	}
	streams := []string{q.Prefix + "tasks"}
	for _, lane := range pipeline.QueueLanes() {
		if err = scanHash(q.Prefix+"ready:"+lane+":tasks", "READY"); err != nil {
			return
		}
		streams = append(streams, q.Prefix+"tasks:"+lane)
	}
	for _, stream := range streams {
		groups, e := q.R.XInfoGroups(ctx, stream).Result()
		if e != nil {
			if strings.Contains(e.Error(), "no such key") {
				continue
			}
			err = e
			return
		}
		last := "0-0"
		groupFound := false
		for _, group := range groups {
			if group.Name == "workers" {
				last = group.LastDeliveredID
				groupFound = true
				break
			}
		}
		if groupFound {
			start := "-"
			for {
				entries, e := q.R.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: stream, Group: "workers", Start: start, End: "+", Count: 100}).Result()
				if e != nil {
					err = e
					return
				}
				for _, v := range entries {
					messages, e := q.R.XRangeN(ctx, stream, v.ID, v.ID, 1).Result()
					if e != nil {
						err = e
						return
					}
					if len(messages) != 1 {
						err = errors.New("pending Redis task payload missing; recover its backup before migration")
						return
					}
					raw, _ := messages[0].Values["task"].(string)
					if err = transfer(stream+":"+v.ID, raw, "READY", time.Time{}); err != nil {
						return
					}
				}
				if len(entries) < 100 {
					break
				}
				start = "(" + entries[len(entries)-1].ID
			}
		}
		for {
			entries, e := q.R.XRangeN(ctx, stream, "("+last, "+", 100).Result()
			if e != nil {
				err = e
				return
			}
			for _, v := range entries {
				raw, _ := v.Values["task"].(string)
				if err = transfer(stream+":"+v.ID, raw, "READY", time.Time{}); err != nil {
					return
				}
				last = v.ID
			}
			if len(entries) < 100 {
				break
			}
		}
	}
	err = q.Broker.MarkLegacyImported(ctx)
	return
}

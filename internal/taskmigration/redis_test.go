package taskmigration

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/pipeline"
	"github.com/KanaDoodle/CampusTrace/internal/testsupport/sqlfixture"
	"github.com/redis/go-redis/v9"
)

func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func TestTaskMigrationPreservesPendingUnreadRetryDeadAndIdempotency(t *testing.T) {
	if os.Getenv("CAMPUS_INTEGRATION") != "1" {
		t.Skip("requires owned SQL and Redis")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dsn := sqlfixture.Database(t, os.Getenv("MYSQL_TEST_DSN"), "task_migration")
	s, e := p.Open(ctx, dsn)
	must(t, e)
	defer s.DB.Close()
	must(t, s.Migrate(ctx))
	r := redis.NewClient(&redis.Options{Addr: os.Getenv("REDIS_ADDR")})
	defer r.Close()
	prefix := "test:migrate:" + d.ID() + ":"
	q, e := pipeline.NewQueue(s.DB, r, prefix, 2, 1, time.Second)
	must(t, e)
	defer q.Close()
	must(t, q.Init(ctx))
	defer func() {
		keys, _ := r.Keys(ctx, prefix+"*").Result()
		if len(keys) > 0 {
			r.Del(ctx, keys...)
		}
	}()
	tasks := []p.Task{}
	for i := 0; i < 6; i++ {
		v := p.NewTask("ASSESS", d.ID())
		v.OwnerID = strings.Repeat("a", 32)
		tasks = append(tasks, v)
	}
	stream := prefix + "tasks"
	ids := []string{}
	for _, task := range tasks[:3] {
		id, e := r.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]any{"task": d.JSON(task)}}).Result()
		must(t, e)
		ids = append(ids, id)
	}
	must(t, r.XGroupCreate(ctx, stream, "workers", "0").Err())
	_, e = r.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "workers", Consumer: "stopped", Streams: []string{stream, ">"}, Count: 2}).Result()
	must(t, e)
	must(t, r.XAck(ctx, stream, "workers", ids[0]).Err())
	must(t, r.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]any{"task": "invalid-private-payload"}}).Err())
	must(t, r.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]any{"task": strings.TrimSuffix(d.JSON(tasks[2]), "}") + `,"secret":"unknown-field"}`}}).Err())
	tasks[3].Attempt = 2
	due := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	must(t, r.ZAdd(ctx, prefix+"retry:ASSESS", redis.Z{Score: float64(due.UnixMilli()), Member: d.JSON(tasks[3])}).Err())
	must(t, r.HSet(ctx, prefix+"dlq", tasks[4].ID, d.JSON(pipeline.Failure{Task: tasks[4], Category: "PERMANENT"})).Err())
	must(t, r.HSet(ctx, prefix+"ready:ASSESS:tasks", tasks[5].ID, d.JSON(tasks[5])).Err())
	if e = q.Init(ctx); e == nil {
		t.Fatal("unmigrated Redis backlog accepted")
	}
	report, e := Run(ctx, q)
	must(t, e)
	if report.Tasks != 5 || report.Poison != 2 {
		t.Fatal(report)
	}
	var count int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM kafka_tasks").Scan(&count))
	if count != 5 {
		t.Fatal("migration lost/duplicated work", count)
	}
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM kafka_tasks WHERE task_id=?", tasks[0].ID).Scan(&count))
	if count != 0 {
		t.Fatal("acknowledged history replayed")
	}
	var body []byte
	var savedDue time.Time
	must(t, s.DB.QueryRowContext(ctx, "SELECT body,due_at FROM kafka_tasks WHERE task_id=? AND state='RETRY'", tasks[3].ID).Scan(&body, &savedDue))
	next, e := pipeline.Decode(body)
	must(t, e)
	if next.Attempt != 2 || !savedDue.Equal(due) {
		t.Fatal("retry progress changed", next.Attempt, savedDue, due)
	}
	// Existing accepted work exceeds the new limit and is preserved; additional
	// work is deferred until completion frees capacity.
	v := p.NewTask("ASSESS", d.ID())
	v.OwnerID = tasks[1].OwnerID
	if e = q.Publish(ctx, v); e != pipeline.ErrQueueFull {
		t.Fatal("post-migration admission", e)
	}
	again, e := Run(ctx, q)
	must(t, e)
	if !again.AlreadyImported || again.Tasks != 0 {
		t.Fatal("migration not idempotent", again)
	}
	must(t, q.Init(ctx))
	if r.XLen(ctx, stream).Val() != 5 {
		t.Fatal("migration destroyed original backup")
	}
}

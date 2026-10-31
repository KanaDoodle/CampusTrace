package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/taskqueue"
	"github.com/redis/go-redis/v9"
	"math/rand/v2"
	"net"
	"os"
	"strings"
	"time"
)

type Queue struct {
	R                       *redis.Client
	Prefix                  string
	Capacity, OwnerCapacity int
	ResolveOwner            func(context.Context, p.Task) (string, error)
	Broker                  *taskqueue.Broker
}

func NewQueue(db *sql.DB, r *redis.Client, prefix string, capacity, ownerCapacity int, lease time.Duration, metrics ...*observability.Metrics) (*Queue, error) {
	if capacity == 0 {
		capacity = 256
	}
	if ownerCapacity == 0 {
		ownerCapacity = min(32, capacity)
	}
	q := &Queue{R: r, Prefix: prefix, Capacity: capacity, OwnerCapacity: ownerCapacity}
	brokers := os.Getenv("KAFKA_BROKERS")
	if brokers == "" {
		brokers = "127.0.0.1:19092"
	}
	topic := os.Getenv("KAFKA_TASK_PREFIX")
	if topic == "" {
		topic = "campustrace.tasks"
	}
	namespace := os.Getenv("KAFKA_TASK_NAMESPACE")
	if namespace == "" {
		namespace = prefix
	}
	var meter *observability.Metrics
	if len(metrics) > 0 {
		meter = metrics[0]
	}
	var e error
	q.Broker, e = taskqueue.New(taskqueue.Config{DB: db, Brokers: strings.Split(brokers, ","), Prefix: topic, Namespace: namespace, Metrics: meter, Lanes: QueueLanes(), Capacity: capacity, OwnerCapacity: ownerCapacity, Lease: lease, Validate: func(m taskqueue.Message) error {
		t, e := Decode(m.Body)
		if e != nil {
			return e
		}
		if t.ID != m.ID || t.OwnerID != m.Owner || taskLane(t) != m.Lane {
			return ErrWrongLane
		}
		return nil
	}})
	return q, e
}
func (q *Queue) Init(ctx context.Context) error {
	if q.Broker == nil {
		return errors.New("Kafka task transport is not configured")
	}
	if e := q.Broker.Init(ctx); e != nil {
		return e
	}
	if e := q.Broker.SetLimits(ctx, q.Capacity, q.OwnerCapacity); e != nil {
		return e
	}
	imported, e := q.Broker.LegacyImported(ctx)
	if e != nil {
		return e
	}
	if !imported && q.R != nil {
		keys := []string{q.Prefix + "tasks", q.Prefix + "dlq"}
		for _, lane := range QueueLanes() {
			keys = append(keys, q.Prefix+"tasks:"+lane, q.Prefix+"ready:"+lane+":tasks")
		}
		for _, kind := range TaskTypes {
			keys = append(keys, q.Prefix+"retry:"+kind)
		}
		count, e := q.R.Exists(ctx, keys...).Result()
		if e != nil {
			return e
		}
		if count > 0 {
			return errors.New("Redis task history detected: stop old writers and run tasks -migrate-redis -writers-stopped before starting Kafka workers")
		}
	}
	return nil
}
func (q *Queue) Close() {
	if q.Broker != nil {
		q.Broker.Close()
	}
}
func (q *Queue) Publish(ctx context.Context, t p.Task) (err error) {
	ctx, span := observability.Producer(observability.Extract(ctx, t.TraceParent), "queue.publish")
	defer func() { observability.End(span, err) }()
	t.TraceParent = observability.TraceParent(ctx)
	if t.OwnerID == "" && q.ResolveOwner != nil {
		t.OwnerID, err = q.ResolveOwner(ctx, t)
		if err != nil {
			return err
		}
	}
	if err = ValidateTask(t); err != nil {
		return err
	}
	if q.Broker == nil {
		return errors.New("Kafka task transport is not configured")
	}
	return q.Broker.Enqueue(ctx, taskqueue.Message{ID: t.ID, Lane: taskLane(t), Owner: t.OwnerID, Body: json.RawMessage(d.JSON(t))})
}
func (q *Queue) Schedule(ctx context.Context) (int, error) { return q.Broker.DispatchOnce(ctx) }
func (q *Queue) TrimAcknowledged(ctx context.Context, retention time.Duration) (int64, error) {
	return q.Broker.Trim(ctx, retention)
}

type LaneStats = taskqueue.LaneStats

func (q *Queue) Stats(ctx context.Context) ([]LaneStats, error) { return q.Broker.Stats(ctx) }

type Failure struct {
	Task        p.Task    `json:"task"`
	Category    string    `json:"error_category"`
	FirstFailed time.Time `json:"first_failed"`
	LastFailed  time.Time `json:"last_failed"`
}

func (q *Queue) DLQ(ctx context.Context) (map[string]string, error) {
	values, e := q.Broker.DeadLetters(ctx)
	if e != nil {
		return nil, e
	}
	out := map[string]string{}
	for k, v := range values {
		out[k] = string(v)
	}
	return out, nil
}
func (q *Queue) Redrive(ctx context.Context, id string) error {
	body, revision, e := q.Broker.DeadTask(ctx, id)
	if e != nil {
		return e
	}
	task, e := Decode(body)
	if e != nil {
		return e
	}
	task.Attempt = 1
	return q.Broker.Redrive(ctx, id, revision, json.RawMessage(d.JSON(task)))
}

func Decode(raw []byte) (p.Task, error) {
	var t p.Task
	if len(raw) > 65536 {
		return t, errors.New("schema: task too large")
	}
	if e := d.Strict(raw, &t); e != nil {
		return t, e
	}
	return t, ValidateTask(t)
}

var TaskTypes = []string{"ANALYZE", "ASSESS", "WATCH_CHECK", "WATCH_FETCH", "SOURCE_IMPORT"}
var ErrQueueFull = taskqueue.ErrFull
var ErrWrongLane = errors.New("task does not belong to this execution lane")

func QueueLanes() []string {
	out := []string{}
	for _, t := range TaskTypes {
		out = append(out, t, "SYSTEM_"+t)
	}
	return out
}
func taskLane(t p.Task) string {
	if t.OwnerID == "" {
		return "SYSTEM_" + t.Type
	}
	return t.Type
}

type queueExpired struct{}

func (queueExpired) Error() string   { return "task queue deadline exceeded before execution" }
func (queueExpired) Transient() bool { return false }

var ErrQueueExpired error = queueExpired{}

type analysisUncertain struct{}

func (analysisUncertain) Error() string   { return "analysis outcome unknown; manual review required" }
func (analysisUncertain) Transient() bool { return false }

var ErrAnalysisUncertain error = analysisUncertain{}

// FailureOutcome preserves task retry budgets while the SQL journal commits
// the transition before the Kafka consumer advances its offset.
func FailureOutcome(t p.Task, err error, maximum int) taskqueue.Outcome {
	if t.FirstFailed.IsZero() {
		t.FirstFailed = time.Now().UTC()
	}
	category := "PERMANENT"
	if Transient(err) {
		category = "TRANSIENT"
	}
	retry := Transient(err) && t.Attempt < maximum && t.Attempt < MaxTaskAttempt
	out := taskqueue.Outcome{State: "DEAD", Category: category}
	if retry {
		t.Attempt++
		out.State = "RETRY"
		out.Delay = Backoff(t.Attempt - 1)
		var pacing interface{ RetryAfter() time.Duration }
		if errors.As(err, &pacing) {
			out.Delay = max(out.Delay, min(time.Hour, pacing.RetryAfter()))
		}
		out.Delay = min(time.Hour, out.Delay)
	}
	out.Body = json.RawMessage(d.JSON(t))
	return out
}

// Redis server time avoids host clock skew; the script records accepted requests only.
var rateScript = redis.NewScript(`local t=redis.call('TIME');local now=t[1]*1000+math.floor(t[2]/1000);redis.call('ZREMRANGEBYSCORE',KEYS[1],'-inf',now-ARGV[1]);if redis.call('ZCARD',KEYS[1])>=tonumber(ARGV[2]) then return 0 end;redis.call('ZADD',KEYS[1],now,ARGV[3]);redis.call('PEXPIRE',KEYS[1],ARGV[1]);return 1`)

func (q *Queue) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	if limit < 1 || window <= 0 {
		return false, errors.New("invalid rate limit")
	}
	n, err := rateScript.Run(ctx, q.R, []string{q.Prefix + "rate:" + key}, window.Milliseconds(), limit, d.ID()).Int()
	return n == 1, err
}

func Transient(err error) bool {
	var classified interface{ Transient() bool }
	if errors.As(err, &classified) {
		return classified.Transient()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var n net.Error
	if errors.As(err, &n) {
		return true
	}
	v := strings.ToLower(err.Error())
	for _, s := range []string{"schema", "validation", "400", "401", "403"} {
		if strings.Contains(v, s) {
			return false
		}
	}
	for _, s := range []string{"timeout", "connection", "network", "429", "500", "502", "503", "504", "rate limit", "no instance", "circuit breaker", "eof", "deadlock", "try restarting transaction"} {
		if strings.Contains(v, s) {
			return true
		}
	}
	return false
}
func Backoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt > 10 {
		attempt = 10
	}
	base := time.Second * time.Duration(1<<attempt)
	return base + time.Duration(rand.Int64N(int64(base/2)+1))
}

const MaxTaskAttempt = 100

func ValidateTask(t p.Task) error {
	if !observability.ValidTraceParent(t.TraceParent) {
		return errors.New("schema: invalid trace context")
	}
	if t.OwnerID != "" {
		if len(t.OwnerID) != 32 {
			return errors.New("schema: invalid task owner")
		}
		for _, c := range t.OwnerID {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return errors.New("schema: invalid task owner")
			}
		}
	}
	if t.Type == "SOURCE_IMPORT" && (len(t.EntityID) != 32 || t.Generation == 0 || t.ID != p.SourceImportTaskID(t.EntityID, t.Generation) || t.ProcessingVersion != "" || t.CorrelationID == "") {
		return errors.New("schema: invalid source import envelope")
	}
	if t.Type == "WATCH_CHECK" || t.Type == "WATCH_FETCH" {
		if len(t.WatchID) != 32 || t.EntityID != t.WatchID || t.ScheduleVersion == 0 || t.CorrelationID == "" {
			return errors.New("schema: invalid watch envelope")
		}
		if t.Type == "WATCH_FETCH" && (t.Posting == nil || t.Posting.SourceID == "" || t.Posting.ExternalID == "" || t.Posting.Text != "") {
			return errors.New("schema: invalid watch posting")
		}
		if t.Type == "WATCH_CHECK" && t.Posting != nil {
			return errors.New("schema: unexpected posting")
		}
	} else if t.WatchID != "" || t.ScheduleVersion != 0 || t.Posting != nil {
		return errors.New("schema: unexpected watch fields")
	}

	if strings.TrimSpace(t.ID) == "" || len(t.ID) > 100 || strings.TrimSpace(t.EntityID) == "" || len(t.EntityID) > 100 || len(t.CorrelationID) > 200 || t.Attempt < 1 || t.Attempt > MaxTaskAttempt || t.Version != 1 || (t.Type != "ANALYZE" && t.Type != "ASSESS" && t.Type != "WATCH_CHECK" && t.Type != "WATCH_FETCH" && t.Type != "SOURCE_IMPORT") {
		return errors.New("schema: invalid task envelope")
	}
	return nil
}

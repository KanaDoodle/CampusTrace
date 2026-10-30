package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/redis/go-redis/v9"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Queue struct {
	Partitioned             bool
	Lane                    string
	Capacity, OwnerCapacity int
	ResolveOwner            func(context.Context, p.Task) (string, error)
	recoveryMu              sync.Mutex
	recoveryCursor          string
	scheduleMu              sync.Mutex
	retryOffsets            map[string]int64
	R                       *redis.Client
	Prefix                  string
}

func (q *Queue) Stream() string {
	if q.Lane != "" {
		return q.Prefix + "tasks:" + q.Lane
	}
	return q.Prefix + "tasks"
}
func (q *Queue) Group() string { return "workers" }
func (q *Queue) Init(ctx context.Context) error {
	if !q.Partitioned && q.Lane == "" {
		// Stop typed producers and drain their queues before a legacy rollback.
		for _, lane := range QueueLanes() {
			keys := q.ForLane(lane).queueKeys()
			count, e := q.R.HLen(ctx, keys[0]).Result()
			if e != nil {
				return e
			}
			length, e := q.R.XLen(ctx, keys[6]).Result()
			if e != nil {
				return e
			}
			if count > 0 || length > 0 {
				return errors.New("typed queues must be drained before legacy rollback")
			}
		}
	}
	if q.Lane != "" && !validLane(q.Lane) {
		return ErrWrongLane
	}
	n, owner := q.limits()
	if n < 1 || n > 4096 || owner < 1 || owner > n {
		return errors.New("queue capacity must be 1..4096 and owner capacity in 1..capacity")
	}
	err := q.R.XGroupCreateMkStream(ctx, q.Stream(), q.Group(), "0").Err()
	if err != nil && strings.Contains(err.Error(), "BUSYGROUP") {
		err = nil
	}
	if err == nil && q.Partitioned && q.Lane == "" {
		for _, name := range QueueLanes() {
			if e := q.ForLane(name).Init(ctx); e != nil {
				return e
			}
		}
	}
	return err
}
func (q *Queue) Publish(ctx context.Context, t p.Task) (err error) {
	ctx, span := observability.Producer(observability.Extract(ctx, t.TraceParent), "queue.publish")
	defer func() { observability.End(span, err) }()
	t.TraceParent = observability.TraceParent(ctx)
	if q.Partitioned || q.Lane != "" {
		return q.publishTyped(ctx, t)
	}
	return q.R.XAdd(ctx, &redis.XAddArgs{Stream: q.Stream(), Values: map[string]any{"task": d.JSON(t)}}).Err()
}
func (q *Queue) Ack(ctx context.Context, id string) error {
	if q.Lane != "" {
		return ackTyped.Run(ctx, q.R, q.releaseKeys([]string{q.Stream(), q.Prefix + "ack-unused", q.Prefix + "dlq", q.marker()}), id, q.Group()).Err()
	}
	return q.R.XAck(ctx, q.Stream(), q.Group(), id).Err()
}
func (q *Queue) Recover(ctx context.Context, consumer string, idle time.Duration) ([]redis.XMessage, error) {
	// Each caller has exactly one free execution slot. Continue across bounded
	// passes; COUNT 1 scans at most 10 PEL entries per Redis command.
	q.recoveryMu.Lock()
	defer q.recoveryMu.Unlock()
	if q.recoveryCursor == "" {
		q.recoveryCursor = "0-0"
	}
	for scan := 0; scan < 32; scan++ {
		msgs, next, err := q.R.XAutoClaim(ctx, &redis.XAutoClaimArgs{Stream: q.Stream(), Group: q.Group(), Consumer: consumer, MinIdle: idle, Start: q.recoveryCursor, Count: 1}).Result()
		if err != nil {
			return nil, err
		}
		q.recoveryCursor = next
		if len(msgs) > 0 || next == "0-0" {
			return msgs, nil
		}
	}
	return nil, nil
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

type Failure struct {
	Task        p.Task    `json:"task"`
	Category    string    `json:"error_category"`
	FirstFailed time.Time `json:"first_failed"`
	LastFailed  time.Time `json:"last_failed"`
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

// Lua serializes this transition but runtime errors do not roll back writes.
// Preflight predictable key/group errors; write destination before completion marker.
// The v2 marker never trusts legacy failed-messages markers written before transfer.
const failureLua = `
local function expect(key,kind)
 local actual=redis.call('TYPE',key).ok
 if actual~='none' and actual~=kind then error('WRONGTYPE '..key) end
end
expect(KEYS[1],'stream');expect(KEYS[4],'set')
local groups=redis.call('XINFO','GROUPS',KEYS[1]);local found=false
for _,g in ipairs(groups) do for i=1,#g,2 do if g[i]=='name' and g[i+1]==ARGV[2] then found=true end end end
if not found then error('NOGROUP') end
if redis.call('SISMEMBER',KEYS[4],ARGV[1])==1 then redis.call('XACK',KEYS[1],ARGV[2],ARGV[1]);if afterAck then afterAck() end;return 0 end
if ARGV[3]=='retry' then expect(KEYS[2],'zset');redis.call('ZADD',KEYS[2],ARGV[4],ARGV[5])
else expect(KEYS[3],'hash');redis.call('HSET',KEYS[3],ARGV[6],ARGV[5]) end
redis.call('SADD',KEYS[4],ARGV[1])
redis.call('XACK',KEYS[1],ARGV[2],ARGV[1]);if afterAck then afterAck() end;return 1`

var failScript = redis.NewScript("local afterAck=nil\n" + failureLua)
var typedFailScript = redis.NewScript(typedReleaseLua + `
local markerType=redis.call('TYPE',KEYS[4]).ok;if markerType~='none' and markerType~='set' then error('WRONGTYPE marker') end
local transferred=redis.call('SISMEMBER',KEYS[4],ARGV[1])==1
local kind=redis.call('TYPE',KEYS[2]).ok
if ARGV[3]=='retry' and kind~='none' and kind~='zset' then error('WRONGTYPE retry') end
if not transferred and ARGV[3]=='retry' and redis.call('ZCARD',KEYS[2])>=tonumber(ARGV[7]) and not redis.call('ZSCORE',KEYS[2],ARGV[5]) then error('RETRY_CAPACITY') end
if ARGV[3]~='retry' then local k=redis.call('TYPE',KEYS[3]).ok;if k~='none' and k~='hash' then error('WRONGTYPE dlq') end
 if not transferred and redis.call('HLEN',KEYS[3])>=10000 and redis.call('HEXISTS',KEYS[3],ARGV[6])==0 then error('DLQ_CAPACITY') end end
` + failureLua)

func (q *Queue) failureTransition() (*redis.Script, []string) {
	keys := []string{q.Stream(), "", q.Prefix + "dlq", q.marker()}
	if q.Lane != "" {
		return typedFailScript, q.releaseKeys(keys)
	}
	return failScript, keys
}

func (q *Queue) Fail(ctx context.Context, msg string, t p.Task, err error, max int) (bool, error) {
	if e := ValidateTask(t); e != nil {
		return false, e
	}
	now := time.Now().UTC()
	// Stable transfer bytes make reruns idempotent even if destination succeeded
	// and a later command failed before the completion marker was recorded.
	transferAt := t.CreatedAt
	if ms, e := strconv.ParseInt(strings.SplitN(msg, "-", 2)[0], 10, 64); e == nil {
		transferAt = time.UnixMilli(ms).UTC()
	}
	if transferAt.IsZero() {
		transferAt = time.Unix(0, 0).UTC()
	}
	if t.FirstFailed.IsZero() {
		t.FirstFailed = transferAt
	}
	retry := Transient(err) && t.Attempt < max && t.Attempt < MaxTaskAttempt
	mode := "dlq"
	category := "PERMANENT"
	value := ""
	if Transient(err) {
		category = "TRANSIENT"
	}
	if retry {
		mode = "retry"
		t.Attempt++
		value = d.JSON(t)
	} else {
		value = d.JSON(Failure{Task: t, Category: category, FirstFailed: t.FirstFailed, LastFailed: transferAt})
	}
	delay := Backoff(t.Attempt - 1)
	var pacing interface{ RetryAfter() time.Duration }
	if errors.As(err, &pacing) {
		if minimum := pacing.RetryAfter(); minimum > delay {
			if minimum > time.Hour {
				minimum = time.Hour
			}
			delay = minimum
		}
	}
	script, keys := q.failureTransition()
	keys[1] = q.Prefix + "retry:" + t.Type
	capacity, _ := q.limits()
	_, e := script.Run(ctx, q.R, keys, msg, q.Group(), mode, now.Add(delay).UnixMilli(), value, t.ID, capacity).Result()
	return retry, e
}

// DeferRateLimited returns a task to the delayed queue without spending its
// network retry budget. A local source pace limit is not a failed fetch.
func (q *Queue) DeferRateLimited(ctx context.Context, msg string, t p.Task) error {
	if err := ValidateTask(t); err != nil {
		return err
	}
	script, keys := q.failureTransition()
	keys[1] = q.Prefix + "retry:" + t.Type
	capacity, _ := q.limits()
	_, err := script.Run(ctx, q.R, keys,
		msg, q.Group(), "retry", time.Now().Add(time.Minute).UnixMilli(), d.JSON(t), t.ID, capacity).Result()
	return err
}

// Each due member is appended before removal; script errors do not roll back writes.
var dueScript = redis.NewScript(`local t=redis.call('TIME');local now=t[1]*1000+math.floor(t[2]/1000);local xs=redis.call('ZRANGEBYSCORE',KEYS[1],'-inf',now,'LIMIT',0,100);for _,v in ipairs(xs) do redis.call('XADD',KEYS[2],'*','task',v);redis.call('ZREM',KEYS[1],v) end;return #xs`)

func (q *Queue) Schedule(ctx context.Context) (int, error) {
	if q.Partitioned {
		return q.scheduleTyped(ctx)
	}
	n := 0
	for _, typ := range []string{"ANALYZE", "ASSESS", "WATCH_CHECK", "WATCH_FETCH", "SOURCE_IMPORT"} {
		v, err := dueScript.Run(ctx, q.R, []string{q.Prefix + "retry:" + typ, q.Stream()}).Int()
		if err != nil {
			return n, err
		}
		n += v
	}
	return n, nil
}
func (q *Queue) DLQ(ctx context.Context) (map[string]string, error) {
	return q.R.HGetAll(ctx, q.Prefix+"dlq").Result()
}

var redriveScript = redis.NewScript(`local v=redis.call('HGET',KEYS[1],ARGV[1]);if not v then return 0 end;local f=cjson.decode(v);f.task.attempt=1;redis.call('XADD',KEYS[2],'*','task',cjson.encode(f.task));redis.call('HDEL',KEYS[1],ARGV[1]);return 1`)

func (q *Queue) Redrive(ctx context.Context, id string) error {
	if q.Partitioned {
		return q.redriveTyped(ctx, id)
	}
	n, err := redriveScript.Run(ctx, q.R, []string{q.Prefix + "dlq", q.Stream()}, id).Int()
	if err == nil && n == 0 {
		return p.ErrNotFound
	}
	return err
}
func Decode(m redis.XMessage) (p.Task, error) {
	var t p.Task
	s, ok := m.Values["task"].(string)
	if !ok {
		return t, fmt.Errorf("schema: task missing")
	}
	if err := d.Strict([]byte(s), &t); err != nil {
		return t, err
	}
	return t, ValidateTask(t)
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

type Poison struct {
	MessageID     string    `json:"message_id"`
	RawSummary    string    `json:"raw_safe_summary"`
	RawHash       string    `json:"raw_hash"`
	DecodeError   string    `json:"decode_error"`
	Timestamp     time.Time `json:"timestamp"`
	Consumer      string    `json:"consumer"`
	CorrelationID string    `json:"correlation_id,omitempty"`
}

var poisonScript = redis.NewScript(`local kind=redis.call('TYPE',KEYS[1]).ok;if kind~='none' and kind~='hash' then error('WRONGTYPE poison') end;redis.call('HSET',KEYS[1],ARGV[1],ARGV[2]);redis.call('XACK',KEYS[2],ARGV[3],ARGV[1]);return 1`)

func (q *Queue) Quarantine(ctx context.Context, m redis.XMessage, t p.Task, decodeErr error, consumer string) error {
	raw, _ := m.Values["task"].(string)
	// Store shape/size and digest, never arbitrary raw text or secrets in logs/DLQ.
	summary := fmt.Sprintf("task bytes=%d; fields=%d", len(raw), len(m.Values))
	correlation := t.CorrelationID
	if correlation == "" && len(raw) <= 65536 {
		var v struct {
			CorrelationID string `json:"correlation_id"`
		}
		_ = json.Unmarshal([]byte(raw), &v)
		correlation = v.CorrelationID
	}
	if len(correlation) > 200 {
		correlation = ""
	}
	detail := decodeErr.Error()
	if len(detail) > 512 {
		detail = detail[:512]
	}
	v := Poison{MessageID: m.ID, RawSummary: summary, RawHash: d.Hash(raw), DecodeError: detail, Timestamp: time.Now().UTC(), Consumer: consumer, CorrelationID: correlation}
	if q.Lane != "" {
		return poisonTyped.Run(ctx, q.R, q.releaseKeys([]string{q.Stream(), q.Prefix + "poison:" + q.Lane, q.Prefix + "dlq", q.marker()}), m.ID, q.Group(), d.JSON(v)).Err()
	}
	return poisonScript.Run(ctx, q.R, []string{q.Prefix + "poison", q.Stream()}, m.ID, d.JSON(v), q.Group()).Err()
}

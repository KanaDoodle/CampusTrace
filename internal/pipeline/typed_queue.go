package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/redis/go-redis/v9"
	"strconv"
	"strings"
	"time"
)

var TaskTypes = []string{"ANALYZE", "ASSESS", "WATCH_CHECK", "WATCH_FETCH", "SOURCE_IMPORT"}
var ErrQueueFull = errors.New("resident task queue is full")
var ErrWrongLane = errors.New("task does not belong to this execution lane")

type queueExpired struct{}

func (queueExpired) Error() string   { return "task queue deadline exceeded before execution" }
func (queueExpired) Transient() bool { return false }

var ErrQueueExpired error = queueExpired{}

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
func (q *Queue) ForLane(lane string) *Queue {
	return &Queue{R: q.R, Prefix: q.Prefix, Partitioned: q.Partitioned, Lane: lane, Capacity: q.Capacity, OwnerCapacity: q.OwnerCapacity, ResolveOwner: q.ResolveOwner}
}
func (q *Queue) queueKeys() []string {
	base := q.Prefix + "ready:" + q.Lane + ":"
	return []string{base + "tasks", base + "owners", base + "counts", base + "turns", base + "scheduled", base + "inflight", q.Stream()}
}
func (q *Queue) listPrefix() string { return q.Prefix + "ready:" + q.Lane + ":tenant:" }
func (q *Queue) limits() (int, int) {
	n, owner := q.Capacity, q.OwnerCapacity
	if n == 0 {
		n = 256
	}
	if owner == 0 {
		owner = 32
	}
	return n, owner
}
func (q *Queue) resolve(ctx context.Context, t p.Task) (p.Task, error) {
	if t.OwnerID == "" && q.ResolveOwner != nil {
		var e error
		t.OwnerID, e = q.ResolveOwner(ctx, t)
		if e != nil {
			return t, e
		}
	}
	return t, ValidateTask(t)
}

// All key types are checked before mutations. Lua does not roll back runtime
// errors; destinations precede source ACK/removal, so an interrupted transfer
// remains recoverable. IDs deduplicate outstanding work, not completed history.
const fairPushLua = `
local function expect(k,t) local a=redis.call('TYPE',k).ok;if a~='none' and a~=t then error('WRONGTYPE '..k) end end
local function push()
 for _,i in ipairs({1,2,3,6}) do expect(KEYS[i],'hash') end
 expect(KEYS[4],'list');expect(KEYS[5],'set');expect(KEYS[7],'stream');expect(ARGV[4]..ARGV[3],'list')
 if redis.call('HEXISTS',KEYS[1],ARGV[2])==1 then return 2 end
 if redis.call('HLEN',KEYS[1])>=tonumber(ARGV[5]) or tonumber(redis.call('HGET',KEYS[3],ARGV[3]) or '0')>=tonumber(ARGV[6]) then return 0 end
 redis.call('HSET',KEYS[1],ARGV[2],ARGV[1]);redis.call('HSET',KEYS[2],ARGV[2],ARGV[3]);redis.call('HINCRBY',KEYS[3],ARGV[3],1)
 redis.call('RPUSH',ARGV[4]..ARGV[3],ARGV[2]);if redis.call('SADD',KEYS[5],ARGV[3])==1 then redis.call('RPUSH',KEYS[4],ARGV[3]) end
 return 1
end
`

var enqueueTyped = redis.NewScript(fairPushLua + `return push()`)
var retryTyped = redis.NewScript(fairPushLua + `
expect(KEYS[8],'zset');local score=redis.call('ZSCORE',KEYS[8],ARGV[7]);if not score then return 0 end
local t=redis.call('TIME');if tonumber(score)>t[1]*1000+math.floor(t[2]/1000) then return 0 end
local n=push();if n>0 then redis.call('ZREM',KEYS[8],ARGV[7]);return 1 end;return 0`)
var legacyTyped = redis.NewScript(fairPushLua + `
expect(KEYS[8],'stream');local xs=redis.call('XPENDING',KEYS[8],ARGV[7],ARGV[8],ARGV[8],1)
if #xs==0 or xs[1][2]~=ARGV[9] then return -1 end
local n=push();if n>0 then redis.call('XACK',KEYS[8],ARGV[7],ARGV[8]);return 1 end;return 0`)
var redriveTyped = redis.NewScript(fairPushLua + `
expect(KEYS[8],'hash');if redis.call('HGET',KEYS[8],ARGV[7])~=ARGV[8] then return -1 end
local n=push();if n>0 then redis.call('HDEL',KEYS[8],ARGV[7]);return 1 end;return 0`)

var poisonRetry = redis.NewScript(`
for _,v in ipairs({{1,'zset'},{2,'hash'}}) do local t=redis.call('TYPE',KEYS[v[1]]).ok;if t~='none' and t~=v[2] then error('WRONGTYPE') end end
if not redis.call('ZSCORE',KEYS[1],ARGV[1]) then return 0 end
redis.call('HSET',KEYS[2],ARGV[2],ARGV[3]);redis.call('ZREM',KEYS[1],ARGV[1]);return 1`)

func (q *Queue) pushArgs(t p.Task) []any {
	n, o := q.limits()
	owner := t.OwnerID
	if owner == "" {
		owner = "system"
	}
	return []any{d.JSON(t), t.ID, owner, q.listPrefix(), n, o}
}
func (q *Queue) publishTyped(ctx context.Context, t p.Task) error {
	var e error
	t, e = q.resolve(ctx, t)
	if e != nil {
		return e
	}
	lane := taskLane(t)
	if q.Lane != "" && q.Lane != lane {
		return ErrWrongLane
	}
	target := q.ForLane(lane)
	n, e := enqueueTyped.Run(ctx, q.R, target.queueKeys(), target.pushArgs(t)...).Int()
	if e != nil {
		return e
	}
	if n == 0 {
		return ErrQueueFull
	}
	return nil
}

var pumpTyped = redis.NewScript(`
for _,i in ipairs({1,2,3,6}) do local t=redis.call('TYPE',KEYS[i]).ok;if t~='none' and t~='hash' then error('WRONGTYPE') end end
for _,v in ipairs({{4,'list'},{5,'set'},{7,'stream'}}) do local t=redis.call('TYPE',KEYS[v[1]]).ok;if t~='none' and t~=v[2] then error('WRONGTYPE') end end
local owner=redis.call('LINDEX',KEYS[4],0);if not owner then return 0 end
local list=ARGV[1]..owner;local t=redis.call('TYPE',list).ok;if t~='none' and t~='list' then error('WRONGTYPE') end
local id=redis.call('LINDEX',list,0);if not id then error('missing ready task') end
local raw=redis.call('HGET',KEYS[1],id);if not raw then error('missing ready payload') end
local msg=redis.call('XADD',KEYS[7],'*','task',raw);redis.call('HSET',KEYS[6],id,msg)
redis.call('LPOP',KEYS[4]);redis.call('LPOP',list);if redis.call('LLEN',list)>0 then redis.call('RPUSH',KEYS[4],owner) else redis.call('SREM',KEYS[5],owner) end
return 1`)

func (q *Queue) Pump(ctx context.Context) (int, error) {
	return pumpTyped.Run(ctx, q.R, q.queueKeys(), q.listPrefix()).Int()
}

// These helpers are also used by failure transfers, after their durable
// destination is written. Stream IDs are scoped by lane throughout.
const typedReleaseLua = `
local function checkTyped()
 for _,i in ipairs({5,6,7,8}) do local t=redis.call('TYPE',KEYS[i]).ok;if t~='none' and t~='hash' then error('WRONGTYPE') end end
end
checkTyped()
local function afterAck()
 local rows=redis.call('XRANGE',KEYS[1],ARGV[1],ARGV[1]);if #rows==0 then return end
 local fields=rows[1][2];local raw=nil;for i=1,#fields,2 do if fields[i]=='task' then raw=fields[i+1] end end
 if raw then local ok,t=pcall(cjson.decode,raw);if ok and t.task_id and redis.call('HGET',KEYS[8],t.task_id)==ARGV[1] then
  local owner=redis.call('HGET',KEYS[6],t.task_id);redis.call('HDEL',KEYS[5],t.task_id);redis.call('HDEL',KEYS[6],t.task_id);redis.call('HDEL',KEYS[8],t.task_id)
  if owner then local n=redis.call('HINCRBY',KEYS[7],owner,-1);if n<=0 then redis.call('HDEL',KEYS[7],owner) end end
 end end
 redis.call('XDEL',KEYS[1],ARGV[1])
 if redis.call('TYPE',KEYS[4]).ok=='set' and redis.call('TTL',KEYS[4])<0 then redis.call('EXPIRE',KEYS[4],172800) end
end
`

var ackTyped = redis.NewScript(typedReleaseLua + `local n=redis.call('XACK',KEYS[1],ARGV[2],ARGV[1]);if n>0 then afterAck() end;return n`)

func (q *Queue) releaseKeys(first []string) []string {
	k := q.queueKeys()
	return append(first, k[0], k[1], k[2], k[5])
}
func (q *Queue) marker() string {
	if q.Lane == "" {
		return q.Prefix + "failure-transferred:v2"
	}
	return q.Prefix + "failure-transferred:v3:" + q.Lane
}
func (q *Queue) BridgeLegacy(ctx context.Context, m redis.XMessage, consumer string) (bool, error) {
	t, e := Decode(m)
	if e != nil {
		return true, q.Quarantine(ctx, m, t, e, consumer)
	}
	t, e = q.resolve(ctx, t)
	if e != nil {
		return false, e
	}
	lane := q.ForLane(taskLane(t))
	keys := append(lane.queueKeys(), q.Stream())
	args := append(lane.pushArgs(t), q.Group(), m.ID, consumer)
	n, e := legacyTyped.Run(ctx, q.R, keys, args...).Int()
	return n != 0, e
}
func (q *Queue) scheduleTyped(ctx context.Context) (int, error) {
	q.scheduleMu.Lock()
	defer q.scheduleMu.Unlock()
	if q.retryOffsets == nil {
		q.retryOffsets = map[string]int64{}
	}
	now, e := q.R.Time(ctx).Result()
	if e != nil {
		return 0, e
	}
	n := 0
	for _, typ := range TaskTypes {
		key := q.Prefix + "retry:" + typ
		due, e := q.R.ZCount(ctx, key, "-inf", fmtMillis(now)).Result()
		if e != nil {
			return n, e
		}
		offset := q.retryOffsets[typ]
		if offset >= due {
			offset = 0
		}
		members, e := q.R.ZRangeByScore(ctx, key, &redis.ZRangeBy{Min: "-inf", Max: fmtMillis(now), Offset: offset, Count: 100}).Result()
		if e != nil {
			return n, e
		}
		q.retryOffsets[typ] = offset + int64(len(members))
		for _, raw := range members {
			t, decodeErr := Decode(redis.XMessage{Values: map[string]any{"task": raw}})
			if decodeErr != nil {
				id := d.Hash(raw)
				v := Poison{MessageID: id, RawHash: id, RawSummary: "retry envelope rejected", DecodeError: "schema: invalid retry envelope", Timestamp: time.Now().UTC(), Consumer: "scheduler"}
				if e = poisonRetry.Run(ctx, q.R, []string{key, q.Prefix + "poison:retry"}, raw, id, d.JSON(v)).Err(); e != nil {
					return n, e
				}
				continue
			}
			t, e = q.resolve(ctx, t)
			if e != nil {
				return n, e
			}
			lane := q.ForLane(taskLane(t))
			keys := append(lane.queueKeys(), key)
			args := append(lane.pushArgs(t), raw)
			count, e := retryTyped.Run(ctx, q.R, keys, args...).Int()
			if e != nil {
				return n, e
			}
			n += count
		}
	}
	return n, nil
}
func fmtMillis(t time.Time) string { return strconv.FormatInt(t.UnixMilli(), 10) }

type LaneStats struct {
	Lane          string `json:"lane"`
	Outstanding   int64  `json:"outstanding"`
	InFlight      int64  `json:"inflight"`
	Ready         int64  `json:"ready"`
	Owners        int64  `json:"owners"`
	Capacity      int64  `json:"capacity"`
	OwnerCapacity int64  `json:"owner_capacity"`
}

func (q *Queue) Stats(ctx context.Context) ([]LaneStats, error) {
	out := []LaneStats{}
	for _, name := range QueueLanes() {
		lane := q.ForLane(name)
		k := lane.queueKeys()
		values, e := q.R.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.HLen(ctx, k[0])
			pipe.HLen(ctx, k[5])
			pipe.HLen(ctx, k[2])
			return nil
		})
		if e != nil {
			return nil, e
		}
		n, o := lane.limits()
		a := values[0].(*redis.IntCmd).Val()
		flight := values[1].(*redis.IntCmd).Val()
		out = append(out, LaneStats{name, a, flight, a - flight, values[2].(*redis.IntCmd).Val(), int64(n), int64(o)})
	}
	return out, nil
}
func (q *Queue) belongs(t p.Task) bool { return q.Lane == "" || q.Lane == taskLane(t) }
func (q *Queue) redriveTyped(ctx context.Context, id string) error {
	raw, e := q.R.HGet(ctx, q.Prefix+"dlq", id).Result()
	if e == redis.Nil {
		return p.ErrNotFound
	}
	if e != nil {
		return e
	}
	var failure Failure
	if e = json.Unmarshal([]byte(raw), &failure); e != nil {
		return e
	}
	t := failure.Task
	t.Attempt = 1
	t.QueueUntil = time.Now().UTC().Add(10 * time.Minute)
	t, e = q.resolve(ctx, t)
	if e != nil {
		return e
	}
	lane := q.ForLane(taskLane(t))
	keys := append(lane.queueKeys(), q.Prefix+"dlq")
	args := append(lane.pushArgs(t), id, raw)
	n, e := redriveTyped.Run(ctx, q.R, keys, args...).Int()
	if e != nil {
		return e
	}
	if n == 0 {
		return ErrQueueFull
	}
	if n < 0 {
		return p.ErrConflict
	}
	return nil
}

var poisonTyped = redis.NewScript(typedReleaseLua + `local t=redis.call('TYPE',KEYS[2]).ok;if t~='none' and t~='hash' then error('WRONGTYPE poison') end;redis.call('HSET',KEYS[2],ARGV[1],ARGV[3]);local n=redis.call('XACK',KEYS[1],ARGV[2],ARGV[1]);if n>0 then afterAck() end;return n`)

func validLane(lane string) bool {
	for _, name := range QueueLanes() {
		if name == lane {
			return true
		}
	}
	return false
}
func isSystemLane(lane string) bool { return strings.HasPrefix(lane, "SYSTEM_") }

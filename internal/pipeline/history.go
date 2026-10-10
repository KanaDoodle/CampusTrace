package pipeline

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis 7.4 has no XTRIM ACKED option. The atomic script protects every group's
// unread boundary and oldest pending entry before trimming whole older nodes.
// SQL outbox/receipts, delayed retries, DLQ and poison records stay untouched.
var trimAcknowledgedScript = redis.NewScript(`
local kind=redis.call('TYPE',KEYS[1]).ok
if kind=='none' then return 0 end
if kind~='stream' then error('WRONGTYPE task stream') end
local groups=redis.call('XINFO','GROUPS',KEYS[1])
if #groups==0 then return 0 end
local t=redis.call('TIME')
local cutoff=string.format('%.0f',t[1]*1000+math.floor(t[2]/1000)-tonumber(ARGV[1]))..'-0'
local function before(a,b)
 local am,as=string.match(a,'^(%d+)%-(%d+)$')
 local bm,bs=string.match(b,'^(%d+)%-(%d+)$')
 if not am or not bm then error('invalid stream boundary') end
 -- Compare decimal strings, preserving 64-bit sequence IDs beyond Lua precision.
 if #am~=#bm then return #am<#bm end
 if am~=bm then return am<bm end
 if #as~=#bs then return #as<#bs end
 return as<bs
end
for _,raw in ipairs(groups) do
 local g={}
 for i=1,#raw,2 do g[raw[i]]=raw[i+1] end
 if not g.name or not g['last-delivered-id'] then error('invalid consumer group') end
 if before(g['last-delivered-id'],cutoff) then cutoff=g['last-delivered-id'] end
 local pending=redis.call('XPENDING',KEYS[1],g.name)
 if pending[1]>0 then
  if not pending[2] then error('invalid pending boundary') end
  if before(pending[2],cutoff) then cutoff=pending[2] end
 end
end
if cutoff=='0-0' then return 0 end
return redis.call('XTRIM',KEYS[1],'MINID','~',cutoff,'LIMIT',10000)
`)

func (q *Queue) TrimAcknowledged(ctx context.Context, retention time.Duration) (int64, error) {
	if retention < time.Hour || retention > 365*24*time.Hour {
		return 0, errors.New("task history retention must be between one hour and one year")
	}
	return trimAcknowledgedScript.Run(ctx, q.R, []string{q.Stream()}, retention.Milliseconds()).Int64()
}

func dispatchDelay(previous time.Duration, work int, failed bool) time.Duration {
	if work > 0 && !failed {
		return 200 * time.Millisecond
	}
	if failed {
		return 3 * time.Second
	}
	return min(3*time.Second, max(200*time.Millisecond, previous*2))
}

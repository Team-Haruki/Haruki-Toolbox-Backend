package upload

import (
	"context"
	"errors"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsoncodec"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	"github.com/redis/go-redis/v9"
)

const (
	birthdayPendingPerSubscription      = 16
	birthdayPendingBytesPerSubscription = 8 << 20
	birthdayPendingGlobal               = 1024
	birthdayPendingBytesGlobal          = 64 << 20
)

var errBirthdayPendingQuota = errors.New("birthday monitor pending-event quota reached")

// The ledger tracks only admitted events. Expired records are reclaimed on
// the next write, and the ledger itself expires with its last event.
var birthdayPendingKeys = []string{
	"haruki:mysekai-birthday:pending-expiry",
	"haruki:mysekai-birthday:pending-size",
	"haruki:mysekai-birthday:pending-usage",
}

const birthdayReleaseLua = `
local function deduct(field, n)
    if redis.call('HINCRBY', KEYS[3], field, -n) == 0 then
        redis.call('HDEL', KEYS[3], field)
    end
end
local function release(key)
    local position = redis.call('HGET', KEYS[2], key)
    if position then
        local entry = cjson.decode(position)
        deduct('count', 1)
        deduct('bytes', entry[2])
        deduct('count:' .. entry[1], 1)
        deduct('bytes:' .. entry[1], entry[2])
        redis.call('HDEL', KEYS[2], key)
    end
    redis.call('ZREM', KEYS[1], key)
    redis.call('DEL', key)
end
`

const birthdayStoreLua = birthdayReleaseLua + `
local raw = redis.call('GET', KEYS[5])
if not raw then return '' end
local monitor = cjson.decode(raw)
if monitor.subscription_id ~= ARGV[2] or monitor.subscription_version ~= ARGV[8] then return '' end
local now = redis.call('TIME')
local ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
for _, key in ipairs(redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ms)) do
    release(key)
end
local key, body, sub = KEYS[4], ARGV[1], ARGV[2]
local previous = redis.call('GET', key)
if previous then return previous end
local size = string.len(body)
local function current(field)
    return tonumber(redis.call('HGET', KEYS[3], field)) or 0
end
if current('count') >= tonumber(ARGV[3]) or
   current('bytes') + size > tonumber(ARGV[4]) or
   current('count:' .. sub) >= tonumber(ARGV[5]) or
   current('bytes:' .. sub) + size > tonumber(ARGV[6]) then
    return false
end
local expires = ms + tonumber(ARGV[7])
redis.call('SET', key, body, 'PX', ARGV[7])
redis.call('ZADD', KEYS[1], expires, key)
redis.call('HSET', KEYS[2], key, cjson.encode({sub, size}))
redis.call('HINCRBY', KEYS[3], 'count', 1)
redis.call('HINCRBY', KEYS[3], 'bytes', size)
redis.call('HINCRBY', KEYS[3], 'count:' .. sub, 1)
redis.call('HINCRBY', KEYS[3], 'bytes:' .. sub, size)
local latest = redis.call('ZREVRANGE', KEYS[1], 0, 0, 'WITHSCORES')
for i = 1, 3 do redis.call('PEXPIREAT', KEYS[i], latest[2]) end
return body
`

const birthdayAckLua = birthdayReleaseLua + `
for i = 4, #KEYS do release(KEYS[i]) end
return 1
`

func storePendingBirthdayEvent(ctx context.Context, client *redis.Client, event BirthdayMonitorEvent, ttl time.Duration) (*BirthdayMonitorEvent, error) {
	body, err := jsoncodec.Marshal(event)
	if err != nil {
		return nil, err
	}
	if len(body) > birthdayPendingBytesPerSubscription {
		return nil, errBirthdayPendingQuota
	}
	keys := make([]string, 0, len(birthdayPendingKeys)+2)
	keys = append(keys, birthdayPendingKeys...)
	keys = append(keys, event.PayloadRef, harukiRedis.BuildMysekaiBirthdayMonitorKey(event.Region, event.UID))
	stored, err := client.Eval(ctx, birthdayStoreLua, keys, body, event.SubscriptionID, birthdayPendingGlobal,
		birthdayPendingBytesGlobal, birthdayPendingPerSubscription, birthdayPendingBytesPerSubscription, ttl.Milliseconds(), event.SubscriptionVersion).Text()
	if errors.Is(err, redis.Nil) {
		return nil, errBirthdayPendingQuota
	}
	if err != nil {
		return nil, err
	}
	if stored == "" {
		return nil, errBirthdayMonitorChanged
	}
	var accepted BirthdayMonitorEvent
	if err := jsoncodec.Unmarshal([]byte(stored), &accepted); err != nil {
		return nil, err
	}
	return &accepted, nil
}

func deletePendingBirthdayEvents(ctx context.Context, client *redis.Client, eventKeys ...string) error {
	keys := make([]string, 0, len(birthdayPendingKeys)+len(eventKeys))
	keys = append(keys, birthdayPendingKeys...)
	keys = append(keys, eventKeys...)
	return client.Eval(ctx, birthdayAckLua, keys).Err()
}

package oauth2

import (
	"context"
	"fmt"
	"math"
	"time"
)

// deviceRateWindow is the fixed window of every non-daily device-flow limit;
// deviceRateDayWindow that of the -day limits.
const (
	deviceRateWindow    = 10 * time.Minute
	deviceRateDayWindow = 24 * time.Hour
)

// deviceRateReserveScript reserves one unit on N fixed-window counters at once.
// ARGV holds a (limit, window ms) pair per key. If any counter is already at
// its limit nothing is incremented and the 1-based index of the first such key
// is returned; otherwise every counter is incremented (setting its expiry on
// the first increment) and {0, count1, …, countN} is returned. Checking all
// counters before writing any keeps a refused request from consuming budget.
const deviceRateReserveScript = `
for i = 1, #KEYS do
  local current = tonumber(redis.call('GET', KEYS[i]) or '0')
  if current >= tonumber(ARGV[2 * i - 1]) then return {i} end
end
local counts = {0}
for i = 1, #KEYS do
  local n = redis.call('INCR', KEYS[i])
  if n == 1 then redis.call('PEXPIRE', KEYS[i], ARGV[2 * i]) end
  counts[#counts + 1] = n
end
return counts
`

// deviceRateReleaseScript gives back one reserved unit per key, as the
// password-reset limiter does: DECR, or DEL when the count would reach zero.
const deviceRateReleaseScript = `
for i = 1, #KEYS do
  local current = redis.call('GET', KEYS[i])
  if current then
    local n = tonumber(current)
    if n == nil or n <= 1 then
      redis.call('DEL', KEYS[i])
    else
      redis.call('DECR', KEYS[i])
    end
  end
end
return 1
`

// deviceRateCounter is one reserved counter.
type deviceRateCounter struct {
	Key    string
	Limit  int
	Window time.Duration
}

// deviceRateReservation is the result of reserve: LimitedIndex is -1 when the
// reservation was taken, otherwise the index of the counter at its limit.
type deviceRateReservation struct {
	LimitedIndex int
	Counts       []int64
}

func (s *deviceFlowStore) reserve(ctx context.Context, counters []deviceRateCounter) (deviceRateReservation, error) {
	manager, err := s.redisManager()
	if err != nil {
		return deviceRateReservation{}, err
	}
	keys := make([]string, 0, len(counters))
	args := make([]any, 0, 2*len(counters))
	for _, counter := range counters {
		keys = append(keys, counter.Key)
		args = append(args, counter.Limit, counter.Window.Milliseconds())
	}
	values, err := manager.Redis.Eval(ctx, deviceRateReserveScript, keys, args...).Int64Slice()
	if err != nil {
		return deviceRateReservation{}, err
	}
	switch {
	case len(values) == 1 && values[0] >= 1 && int(values[0]) <= len(counters):
		return deviceRateReservation{LimitedIndex: int(values[0]) - 1}, nil
	case len(values) == len(counters)+1 && values[0] == 0:
		return deviceRateReservation{LimitedIndex: -1, Counts: values[1:]}, nil
	}
	return deviceRateReservation{}, fmt.Errorf("unexpected device rate reservation result of %d values", len(values))
}

func (s *deviceFlowStore) release(ctx context.Context, counters []deviceRateCounter) error {
	manager, err := s.redisManager()
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(counters))
	for _, counter := range counters {
		keys = append(keys, counter.Key)
	}
	return manager.Redis.Eval(ctx, deviceRateReleaseScript, keys).Err()
}

// increment counts one event on a counter that never refuses (the warn-only
// counters), returning the new count.
func (s *deviceFlowStore) increment(ctx context.Context, key string, window time.Duration) (int64, error) {
	manager, err := s.redisManager()
	if err != nil {
		return 0, err
	}
	return manager.IncrementWithTTL(ctx, key, window)
}

// retryAfterSeconds is the remaining window of a counter, at least 1 s, or the
// full window when the TTL cannot be read.
func (s *deviceFlowStore) retryAfterSeconds(ctx context.Context, key string, window time.Duration) int {
	fallback := int(window.Seconds())
	manager, err := s.redisManager()
	if err != nil {
		return fallback
	}
	ttl, err := manager.Redis.PTTL(ctx, key).Result()
	if err != nil || ttl <= 0 {
		return fallback
	}
	return max(1, int(math.Ceil(ttl.Seconds())))
}

// deviceWarnThreshold is the count at which a reserved pool logs its warning:
// half of its limit.
func deviceWarnThreshold(limit int) int64 {
	return int64(max(1, limit/2))
}

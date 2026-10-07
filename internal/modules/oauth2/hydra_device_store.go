package oauth2

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"

	"github.com/redis/go-redis/v9"
)

// Device flow states (design §7.4). issued, denied, failed and expired are
// terminal; approving, approved and unconfirmed form the "approved class" whose
// tokens the token endpoint may hand out.
const (
	deviceFlowStatePending     = "pending"
	deviceFlowStateClaimed     = "claimed"
	deviceFlowStateApproving   = "approving"
	deviceFlowStateApproved    = "approved"
	deviceFlowStateUnconfirmed = "unconfirmed"
	deviceFlowStateIssued      = "issued"
	deviceFlowStateDenied      = "denied"
	deviceFlowStateFailed      = "failed"
	deviceFlowStateExpired     = "expired"

	deviceFlowRecordVersion = "1"
	// deviceFlowIssuedRetention is how long a flow and its dc index live after
	// its tokens were handed out.
	deviceFlowIssuedRetention = 300 * time.Second
	// deviceFlowConsentRequestRetention is how long the crid key of an
	// unredeemed flow outlives the flow HASH, and the TTL every failed reaper
	// revocation renews it to. The key is deleted whenever the flow leaves the
	// unredeemed set, so this only bounds how long the reaper may be stopped
	// before a still-unrevoked consent is forgotten.
	deviceFlowConsentRequestRetention = 7 * 24 * time.Hour
)

// errDeviceStoreUnavailable means Redis is not configured; callers answer 503.
var errDeviceStoreUnavailable = errors.New("device flow store is unavailable")

func isDeviceFlowApprovedClass(state string) bool {
	return state == deviceFlowStateApproving || state == deviceFlowStateApproved || state == deviceFlowStateUnconfirmed
}

// Every script first applies the lazy expiry of pending and claimed flows
// (now >= exp), with now passed in milliseconds as ARGV so tests control the
// clock. Field names are fixed by design §7.3.
const deviceFlowLazyExpiryLua = `
local function lazy_expire(flow, now)
  local st = redis.call('HGET', flow, 'st')
  if (st == 'pending' or st == 'claimed') and now >= (tonumber(redis.call('HGET', flow, 'exp')) or 0) then
    redis.call('HSET', flow, 'st', 'expired')
    return 'expired'
  end
  return st or ''
end
`

// deviceFlowCreateScript: KEYS flow, dc, uc; ARGV fid, expires_in ms,
// record-grace ms, then field/value pairs. The user-code index is claimed with
// SET NX first so a collision writes neither the flow nor the dc index.
const deviceFlowCreateScript = `
if not redis.call('SET', KEYS[3], ARGV[1], 'NX', 'PX', ARGV[2]) then
  return 'UC_COLLISION'
end
local ttl = tonumber(ARGV[2]) + tonumber(ARGV[3])
local fields = {}
for i = 4, #ARGV do fields[#fields + 1] = ARGV[i] end
redis.call('HSET', KEYS[1], unpack(fields))
redis.call('PEXPIRE', KEYS[1], ttl)
redis.call('SET', KEYS[2], ARGV[1], 'PX', ttl)
return 'OK'
`

// deviceFlowPollScript: KEYS flow; ARGV now ms, requesting client, max interval
// s, max slow_down count. A missing flow returns nil. The early-poll test only
// looks at lpoll, which every poll (forwarded or not) moves to now, so a client
// that ignores slow_down keeps getting it; pending and claimed flows fail after
// more than max slow_downs.
const deviceFlowPollScript = deviceFlowLazyExpiryLua + `
if redis.call('EXISTS', KEYS[1]) == 0 then return nil end
local now = tonumber(ARGV[1])
if redis.call('HGET', KEYS[1], 'cid') ~= ARGV[2] then return {'CLIENT_MISMATCH'} end
local st = lazy_expire(KEYS[1], now)
local ivl = tonumber(redis.call('HGET', KEYS[1], 'ivl')) or 5
local lpoll = tonumber(redis.call('HGET', KEYS[1], 'lpoll')) or 0
redis.call('HSET', KEYS[1], 'lpoll', now)
if now - lpoll < ivl * 1000 - 1000 then
  ivl = math.min(ivl + 5, tonumber(ARGV[3]))
  local sdn = redis.call('HINCRBY', KEYS[1], 'sdn', 1)
  redis.call('HSET', KEYS[1], 'ivl', ivl)
  if (st == 'pending' or st == 'claimed') and sdn > tonumber(ARGV[4]) then
    redis.call('HSET', KEYS[1], 'st', 'failed')
  end
  return {'SLOW_DOWN', tostring(ivl)}
end
local f = redis.call('HMGET', KEYS[1], 'exp', 'wdc', 'crid')
return {'FORWARD', st, f[1] or '0', f[2] or '', f[3] or ''}
`

// deviceFlowSettleScript: KEYS flow, unredeemed, dc, crid; ARGV now ms, Hydra result
// (ok, pending, expired_token, invalid_grant, other), client active ("1", "0",
// or "" when not yet known), issued retention ms, fid. It returns
// {action, state, crid, cby}; see deviceSettle* for the actions and design §6.3
// for the table it implements. A "" client state in a branch that needs it
// returns CHECK_CLIENT without writing anything.
const deviceFlowSettleScript = deviceFlowLazyExpiryLua + `
if redis.call('EXISTS', KEYS[1]) == 0 then return {'GONE', '', '', ''} end
local now = tonumber(ARGV[1])
local st = lazy_expire(KEYS[1], now)
local f = redis.call('HMGET', KEYS[1], 'exp', 'crid', 'cby')
local exp = tonumber(f[1]) or 0
local crid = f[2] or ''
local cby = f[3] or ''
local approved = st == 'approving' or st == 'approved' or st == 'unconfirmed'
local result, active = ARGV[2], ARGV[3]
local function set_state(s) redis.call('HSET', KEYS[1], 'st', s); st = s end
if result == 'ok' then
  if approved then
    if active == '' then return {'CHECK_CLIENT', st, crid, cby} end
    if active == '1' then
      set_state('issued')
      redis.call('HSET', KEYS[1], 'ist', now)
      redis.call('ZREM', KEYS[2], ARGV[5])
      redis.call('DEL', KEYS[4])
      redis.call('PEXPIRE', KEYS[1], ARGV[4])
      redis.call('PEXPIRE', KEYS[3], ARGV[4])
      return {'TOKEN', st, crid, cby}
    end
    set_state('denied')
    redis.call('HSET', KEYS[1], 'dres', 'client_disabled')
    return {'REVOKE_DENIED', st, crid, cby}
  end
  if st == 'issued' then return {'PASS', st, crid, cby} end
  if st == 'denied' then return {'REVOKE_TERMINAL_DENIED', st, crid, cby} end
  return {'REVOKE_TERMINAL_EXPIRED', st, crid, cby}
elseif result == 'pending' then
  if st == 'denied' then return {'ACCESS_DENIED', st, crid, cby} end
  if st == 'failed' then return {'EXPIRED_TOKEN', st, crid, cby} end
  if st == 'expired' or (st ~= 'issued' and now >= exp) then
    if st ~= 'expired' then set_state('expired') end
    return {'EXPIRED_TOKEN', st, crid, cby}
  end
  return {'PASS', st, crid, cby}
elseif result == 'expired_token' then
  if st == 'pending' or st == 'claimed' or approved then set_state('expired') end
  return {'EXPIRED_TOKEN', st, crid, cby}
elseif result == 'invalid_grant' then
  if st == 'denied' then return {'ACCESS_DENIED', st, crid, cby} end
  if approved then
    if active == '' then return {'CHECK_CLIENT', st, crid, cby} end
    redis.call('ZREM', KEYS[2], ARGV[5])
    redis.call('DEL', KEYS[4])
    if active == '0' then
      set_state('denied')
      redis.call('HSET', KEYS[1], 'dres', 'client_disabled')
      return {'ACCESS_DENIED', st, crid, cby}
    end
    set_state('expired')
    return {'PASS', st, crid, cby}
  end
  if st == 'expired' then return {'EXPIRED_TOKEN', st, crid, cby} end
end
return {'PASS', st, crid, cby}
`

// deviceFlowReapScript: KEYS unredeemed, flow, crid; ARGV fid. The ZREM is the
// claim, so with several instances only one handles a flow. It returns
// {REVOKE, crid} only when the claim succeeded, the flow was not issued and a
// crid is known. The crid key outlives the flow HASH, so a flow whose
// revocation kept failing past the HASH's TTL is still revoked rather than
// dropped as GONE.
const deviceFlowReapScript = `
if redis.call('ZREM', KEYS[1], ARGV[1]) == 0 then return {'SKIP', ''} end
local kept = redis.call('GET', KEYS[3]) or ''
if redis.call('EXISTS', KEYS[2]) == 0 then
  if kept == '' then return {'GONE', ''} end
  return {'REVOKE', kept}
end
local f = redis.call('HMGET', KEYS[2], 'st', 'crid')
if f[1] == 'issued' then
  redis.call('DEL', KEYS[3])
  return {'ISSUED', ''}
end
local crid = f[2] or ''
if crid == '' then crid = kept end
if crid == '' then return {'MARK', ''} end
return {'REVOKE', crid}
`

// deviceFlowRequeueScript: KEYS unredeemed, crid; ARGV score ms, fid, crid,
// crid retention ms. Puts a flow whose revocation failed back into the set
// and renews its crid key, so the retry survives the flow HASH's expiry.
const deviceFlowRequeueScript = `
redis.call('ZADD', KEYS[1], ARGV[1], ARGV[2])
redis.call('SET', KEYS[2], ARGV[3], 'PX', ARGV[4])
return 'OK'
`

// deviceFlowRemoveUnredeemedScript: KEYS unredeemed, crid; ARGV fid.
const deviceFlowRemoveUnredeemedScript = `
redis.call('ZREM', KEYS[1], ARGV[1])
redis.call('DEL', KEYS[2])
return 'OK'
`

// deviceFlowMarkExpiredScript: KEYS flow, crid. Ends a reaped flow: its crid
// key is deleted; non-terminal states become expired; issued, denied, failed
// and expired are left alone, as is a flow HASH that already expired.
const deviceFlowMarkExpiredScript = `
redis.call('DEL', KEYS[2])
local st = redis.call('HGET', KEYS[1], 'st')
if not st then return '' end
if st == 'pending' or st == 'claimed' or st == 'approving' or st == 'approved' or st == 'unconfirmed' then
  redis.call('HSET', KEYS[1], 'st', 'expired')
  return 'expired'
end
return st
`

// Settle actions returned by deviceFlowSettleScript.
const (
	deviceSettleToken                 = "TOKEN"
	deviceSettlePass                  = "PASS"
	deviceSettleAccessDenied          = "ACCESS_DENIED"
	deviceSettleExpiredToken          = "EXPIRED_TOKEN"
	deviceSettleRevokeDenied          = "REVOKE_DENIED"
	deviceSettleRevokeTerminalDenied  = "REVOKE_TERMINAL_DENIED"
	deviceSettleRevokeTerminalExpired = "REVOKE_TERMINAL_EXPIRED"
	deviceSettleCheckClient           = "CHECK_CLIENT"
	deviceSettleGone                  = "GONE"
)

// Hydra token-endpoint results passed to deviceFlowSettleScript.
const (
	deviceHydraResultOK           = "ok"
	deviceHydraResultPending      = "pending"
	deviceHydraResultExpiredToken = "expired_token"
	deviceHydraResultInvalidGrant = "invalid_grant"
	deviceHydraResultOther        = "other"
)

// deviceFlowStore keeps device flows in Redis. It holds the DB manager and
// reads its Redis client on every call, so it can be built before (or without)
// Redis, as the route-manifest test does; a nil client is
// errDeviceStoreUnavailable. It also owns the short-lived Hydra client cache
// shared by device/auth and the token endpoint.
type deviceFlowStore struct {
	db      *database.HarukiToolboxDBManager
	now     func() time.Time
	clients *deviceClientCache
}

func newDeviceFlowStore(db *database.HarukiToolboxDBManager) *deviceFlowStore {
	store := &deviceFlowStore{db: db, now: time.Now}
	store.clients = newDeviceClientCache(func() time.Time { return store.now() })
	return store
}

func (s *deviceFlowStore) redisManager() (*harukiRedis.HarukiRedisManager, error) {
	if s == nil || s.db == nil || s.db.Redis == nil || s.db.Redis.Redis == nil {
		return nil, errDeviceStoreUnavailable
	}
	return s.db.Redis, nil
}

func (s *deviceFlowStore) nowMillis() int64 { return s.now().UnixMilli() }

// deviceFlowRecord is what device/auth writes when a flow is created.
type deviceFlowRecord struct {
	FlowID             string
	WrappedDeviceCode  string
	NormalizedUserCode string
	ClientID           string
	ClientType         string
	Scope              string
	DeviceLabel        string
	SealedDeviceCode   string
	CreatedAt          time.Time
	ExpiresIn          time.Duration
	IntervalSeconds    int
}

// create stores a new pending flow. It returns false when the user code is
// already in use, in which case nothing was written.
func (s *deviceFlowStore) create(ctx context.Context, record deviceFlowRecord, recordGrace time.Duration) (bool, error) {
	manager, err := s.redisManager()
	if err != nil {
		return false, err
	}
	keys := manager.KeyBuilder()
	created := record.CreatedAt.UnixMilli()
	expiresInMillis := record.ExpiresIn.Milliseconds()
	args := []any{
		record.FlowID, expiresInMillis, recordGrace.Milliseconds(),
		"v", deviceFlowRecordVersion,
		"cid", record.ClientID,
		"ctype", record.ClientType,
		"scope", record.Scope,
		"dlb", record.DeviceLabel,
		"uch", keys.HashOAuth2DeviceIdentifier("uc", record.NormalizedUserCode),
		"wdc", record.SealedDeviceCode,
		"crt", created,
		"exp", created + expiresInMillis,
		"ivl", record.IntervalSeconds,
		"lpoll", 0,
		"sdn", 0,
		"st", deviceFlowStatePending,
	}
	result, err := manager.Redis.Eval(ctx, deviceFlowCreateScript, []string{
		keys.BuildOAuth2DeviceFlowKey(record.FlowID),
		keys.BuildOAuth2DeviceCodeIndexKey(record.WrappedDeviceCode),
		keys.BuildOAuth2DeviceUserCodeIndexKey(record.NormalizedUserCode),
	}, args...).Text()
	if err != nil {
		return false, err
	}
	switch result {
	case "OK":
		return true, nil
	case "UC_COLLISION":
		return false, nil
	default:
		return false, fmt.Errorf("unexpected device flow create result %q", result)
	}
}

// flowIDForDeviceCode resolves a wrapped device code through the dc index.
func (s *deviceFlowStore) flowIDForDeviceCode(ctx context.Context, wrappedDeviceCode string) (string, bool, error) {
	manager, err := s.redisManager()
	if err != nil {
		return "", false, err
	}
	flowID, err := manager.Redis.Get(ctx, manager.KeyBuilder().BuildOAuth2DeviceCodeIndexKey(wrappedDeviceCode)).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return flowID, true, nil
}

type devicePollOutcome int

const (
	devicePollMissing devicePollOutcome = iota
	devicePollClientMismatch
	devicePollSlowDown
	devicePollForward
)

type devicePollResult struct {
	Outcome          devicePollOutcome
	State            string
	ExpiresAtMillis  int64
	SealedDeviceCode string
	ConsentRequestID string
	IntervalSeconds  int
}

func (s *deviceFlowStore) poll(ctx context.Context, flowID, clientID string, maxIntervalSeconds, maxSlowDown int) (devicePollResult, error) {
	manager, err := s.redisManager()
	if err != nil {
		return devicePollResult{}, err
	}
	values, err := manager.Redis.Eval(ctx, deviceFlowPollScript,
		[]string{manager.KeyBuilder().BuildOAuth2DeviceFlowKey(flowID)},
		s.nowMillis(), clientID, maxIntervalSeconds, maxSlowDown,
	).StringSlice()
	if errors.Is(err, redis.Nil) {
		return devicePollResult{Outcome: devicePollMissing}, nil
	}
	if err != nil {
		return devicePollResult{}, err
	}
	switch {
	case len(values) == 1 && values[0] == "CLIENT_MISMATCH":
		return devicePollResult{Outcome: devicePollClientMismatch}, nil
	case len(values) == 2 && values[0] == "SLOW_DOWN":
		interval, convErr := strconv.Atoi(values[1])
		if convErr != nil {
			return devicePollResult{}, fmt.Errorf("invalid slow_down interval: %w", convErr)
		}
		return devicePollResult{Outcome: devicePollSlowDown, IntervalSeconds: interval}, nil
	case len(values) == 5 && values[0] == "FORWARD":
		expiresAt, convErr := strconv.ParseInt(values[2], 10, 64)
		if convErr != nil {
			return devicePollResult{}, fmt.Errorf("invalid flow expiry: %w", convErr)
		}
		return devicePollResult{
			Outcome:          devicePollForward,
			State:            values[1],
			ExpiresAtMillis:  expiresAt,
			SealedDeviceCode: values[3],
			ConsentRequestID: values[4],
		}, nil
	}
	return devicePollResult{}, fmt.Errorf("unexpected device poll result of %d values", len(values))
}

type deviceSettleResult struct {
	Action           string
	State            string
	ConsentRequestID string
	ClaimedBy        string
}

// settle records what Hydra answered a forwarded poll. clientActive is "1" or
// "0" once known and "" otherwise.
func (s *deviceFlowStore) settle(ctx context.Context, flowID, wrappedDeviceCode, hydraResult, clientActive string) (deviceSettleResult, error) {
	manager, err := s.redisManager()
	if err != nil {
		return deviceSettleResult{}, err
	}
	keys := manager.KeyBuilder()
	values, err := manager.Redis.Eval(ctx, deviceFlowSettleScript, []string{
		keys.BuildOAuth2DeviceFlowKey(flowID),
		keys.BuildOAuth2DeviceUnredeemedKey(),
		keys.BuildOAuth2DeviceCodeIndexKey(wrappedDeviceCode),
		keys.BuildOAuth2DeviceConsentRequestKey(flowID),
	}, s.nowMillis(), hydraResult, clientActive, deviceFlowIssuedRetention.Milliseconds(), flowID).StringSlice()
	if err != nil {
		return deviceSettleResult{}, err
	}
	if len(values) != 4 {
		return deviceSettleResult{}, fmt.Errorf("unexpected device settle result of %d values", len(values))
	}
	return deviceSettleResult{Action: values[0], State: values[1], ConsentRequestID: values[2], ClaimedBy: values[3]}, nil
}

// removeUnredeemed drops a flow from the unredeemed set, and its crid key,
// after its consent session was revoked.
func (s *deviceFlowStore) removeUnredeemed(ctx context.Context, flowID string) error {
	manager, err := s.redisManager()
	if err != nil {
		return err
	}
	keys := manager.KeyBuilder()
	return manager.Redis.Eval(ctx, deviceFlowRemoveUnredeemedScript, []string{
		keys.BuildOAuth2DeviceUnredeemedKey(),
		keys.BuildOAuth2DeviceConsentRequestKey(flowID),
	}, flowID).Err()
}

// dueUnredeemed lists up to limit flows whose score (exp in ms) is below cutoff.
func (s *deviceFlowStore) dueUnredeemed(ctx context.Context, cutoffMillis int64, limit int64) ([]string, error) {
	manager, err := s.redisManager()
	if err != nil {
		return nil, err
	}
	return manager.Redis.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key:     manager.KeyBuilder().BuildOAuth2DeviceUnredeemedKey(),
		Start:   "-inf",
		Stop:    "(" + strconv.FormatInt(cutoffMillis, 10),
		ByScore: true,
		Count:   limit,
	}).Result()
}

// reap claims a due flow. The action is REVOKE (with the consent request ID to
// revoke, also when the flow HASH already expired), MARK (no consent request
// ID: only mark it expired), or ISSUED, GONE and SKIP (nothing to do).
func (s *deviceFlowStore) reap(ctx context.Context, flowID string) (action string, consentRequestID string, err error) {
	manager, err := s.redisManager()
	if err != nil {
		return "", "", err
	}
	keys := manager.KeyBuilder()
	values, err := manager.Redis.Eval(ctx, deviceFlowReapScript, []string{
		keys.BuildOAuth2DeviceUnredeemedKey(),
		keys.BuildOAuth2DeviceFlowKey(flowID),
		keys.BuildOAuth2DeviceConsentRequestKey(flowID),
	}, flowID).StringSlice()
	if err != nil {
		return "", "", err
	}
	if len(values) != 2 {
		return "", "", fmt.Errorf("unexpected device reap result of %d values", len(values))
	}
	return values[0], values[1], nil
}

// markExpired ends a reaped flow: it marks a non-terminal flow expired and
// deletes the flow's crid key.
func (s *deviceFlowStore) markExpired(ctx context.Context, flowID string) error {
	manager, err := s.redisManager()
	if err != nil {
		return err
	}
	keys := manager.KeyBuilder()
	return manager.Redis.Eval(ctx, deviceFlowMarkExpiredScript, []string{
		keys.BuildOAuth2DeviceFlowKey(flowID),
		keys.BuildOAuth2DeviceConsentRequestKey(flowID),
	}).Err()
}

// requeueUnredeemed puts a flow back into the unredeemed set after a failed
// revocation, so a later reaper round retries it, and renews its crid key so
// the retry does not depend on the flow HASH still existing.
func (s *deviceFlowStore) requeueUnredeemed(ctx context.Context, flowID, consentRequestID string, scoreMillis int64) error {
	manager, err := s.redisManager()
	if err != nil {
		return err
	}
	keys := manager.KeyBuilder()
	return manager.Redis.Eval(ctx, deviceFlowRequeueScript, []string{
		keys.BuildOAuth2DeviceUnredeemedKey(),
		keys.BuildOAuth2DeviceConsentRequestKey(flowID),
	}, scoreMillis, flowID, consentRequestID, deviceFlowConsentRequestRetention.Milliseconds()).Err()
}

// deviceClientCacheTTL is how long a Hydra client lookup (found or 404) is
// reused, so an anonymous device/auth flood costs at most one admin GET per
// client ID every 5 s. Admin changes therefore take up to 5 s to apply.
const (
	deviceClientCacheTTL        = 5 * time.Second
	deviceClientCacheMaxEntries = 4096
)

type deviceClientCacheEntry struct {
	client *HydraOAuthClient
	at     time.Time
}

type deviceClientCache struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]deviceClientCacheEntry
}

func newDeviceClientCache(now func() time.Time) *deviceClientCache {
	return &deviceClientCache{now: now, entries: make(map[string]deviceClientCacheEntry)}
}

// get returns the client, nil when Hydra answered 404, or the lookup error.
// Errors are not cached.
func (c *deviceClientCache) get(ctx context.Context, lookup func(context.Context, string) (*HydraOAuthClient, error), clientID string) (*HydraOAuthClient, error) {
	now := c.now()
	c.mu.Lock()
	entry, ok := c.entries[clientID]
	c.mu.Unlock()
	if ok && now.Sub(entry.at) < deviceClientCacheTTL {
		return entry.client, nil
	}
	client, err := lookup(ctx, clientID)
	if err != nil {
		if !IsHydraNotFoundError(err) {
			return nil, err
		}
		client = nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= deviceClientCacheMaxEntries {
		for id, cached := range c.entries {
			if now.Sub(cached.at) >= deviceClientCacheTTL {
				delete(c.entries, id)
			}
		}
	}
	// A flood of distinct unknown IDs must not grow the map without bound;
	// past the cap a lookup is simply not cached.
	if len(c.entries) < deviceClientCacheMaxEntries {
		c.entries[clientID] = deviceClientCacheEntry{client: client, at: now}
	}
	return client, nil
}

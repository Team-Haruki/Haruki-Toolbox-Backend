package oauth2

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Result codes of the browser-side device-flow scripts (design §7.5).
const (
	deviceClaimNew           = "CLAIMED_NEW"
	deviceClaimRenewed       = "CLAIMED_RENEWED"
	deviceClaimTaken         = "TAKEN"
	deviceClaimExpired       = "EXPIRED"
	deviceClaimExpiredOwn    = "EXPIRED_OWN"
	deviceClaimHandledOwn    = "HANDLED_OWN"
	deviceClaimInProgressOwn = "IN_PROGRESS_OWN"
	deviceClaimMissing       = "MISSING"

	deviceDecisionOK             = "OK"
	deviceDecisionHandleMismatch = "HANDLE_MISMATCH"
	deviceDecisionSessionChanged = "SESSION_CHANGED"
	deviceDecisionNotClaimer     = "NOT_CLAIMER"
	deviceDecisionInProgress     = "IN_PROGRESS"
	deviceDecisionHandled        = "HANDLED"
	deviceDecisionTooLate        = "TOO_LATE"
	deviceDecisionExpired        = "EXPIRED"
	deviceDecisionMaxAttempts    = "MAX_ATTEMPTS"

	deviceFinishAlreadyIssued = "ALREADY_ISSUED"
	deviceNonceMismatch       = "NONCE_MISMATCH"
)

// Modes of deviceFlowFinishApproveScript.
const (
	deviceFinishApproved    = "approved"
	deviceFinishUnconfirmed = "unconfirmed"
	// deviceFinishRevert is only used once Hydra is known to hold no completed
	// consent for the attempt; it also drops the flow from the unredeemed set.
	deviceFinishRevert = "revert"
	deviceFinishFailed = "failed"
)

// deviceFlowClaimScript: KEYS uc, fh (the new handle's index); ARGV now ms,
// uid, csh, hnd, claim TTL ms, flow key prefix. The flow key is derived from
// the uc index inside the script (single-node Redis only), so "missing",
// "expired" and "taken by someone else" cost exactly one round trip each and
// cannot be told apart by timing, and there is no GET-then-EVAL race.
// A claim returns {code, fid, cid, ctype, scope, dlb, crt, exp}; every other
// result {code, fid?}.
const deviceFlowClaimScript = deviceFlowLazyExpiryLua + `
local fid = redis.call('GET', KEYS[1])
if not fid then return {'MISSING'} end
local flow = ARGV[6] .. fid
if redis.call('EXISTS', flow) == 0 then return {'MISSING'} end
local now = tonumber(ARGV[1])
local st = lazy_expire(flow, now)
local cby = redis.call('HGET', flow, 'cby') or ''
local own = cby == ARGV[2]
local function claim(code)
  local exp = tonumber(redis.call('HGET', flow, 'exp')) or 0
  local cuntil = math.min(now + tonumber(ARGV[5]), exp)
  redis.call('HSET', flow, 'st', 'claimed', 'cby', ARGV[2], 'csh', ARGV[3], 'hnd', ARGV[4], 'cuntil', cuntil)
  redis.call('SET', KEYS[2], fid, 'PX', math.max(1, cuntil - now))
  local f = redis.call('HMGET', flow, 'cid', 'ctype', 'scope', 'dlb', 'crt', 'exp')
  return {code, fid, f[1] or '', f[2] or '', f[3] or '', f[4] or '', f[5] or '0', f[6] or '0'}
end
if st == 'pending' then return claim('CLAIMED_NEW') end
if st == 'claimed' then
  if own then return claim('CLAIMED_RENEWED') end
  if (tonumber(redis.call('HGET', flow, 'cuntil')) or 0) < now then return claim('CLAIMED_NEW') end
  return {'TAKEN', fid}
end
if st == 'approving' then
  if own then return {'IN_PROGRESS_OWN', fid} end
  return {'TAKEN', fid}
end
if st == 'expired' then
  if own then return {'EXPIRED_OWN', fid} end
  if cby == '' then return {'EXPIRED', fid} end
  return {'TAKEN', fid}
end
if own then return {'HANDLED_OWN', fid} end
-- A flow that failed before anyone claimed it (too many slow_downs).
if cby == '' then return {'EXPIRED', fid} end
return {'TAKEN', fid}
`

// deviceFlowDecisionGuardLua checks that a decision (approve or deny) comes
// from the claimer, with the current handle, in the claiming session, while
// the flow can still be decided: claimed, or approving whose lease has run
// out. It returns ” when the decision may proceed.
const deviceFlowDecisionGuardLua = `
local function decision_guard(flow, now, uid, csh, hnd)
  local st = lazy_expire(flow, now)
  local f = redis.call('HMGET', flow, 'cby', 'hnd', 'csh', 'auntil')
  if (f[2] or '') ~= hnd then return 'HANDLE_MISMATCH' end
  if (f[1] or '') ~= uid then return 'NOT_CLAIMER' end
  if (f[3] or '') ~= csh then return 'SESSION_CHANGED' end
  if st == 'expired' then return 'EXPIRED' end
  if st == 'approving' then
    if (tonumber(f[4]) or 0) >= now then return 'IN_PROGRESS' end
    return ''
  end
  if st == 'claimed' then return '' end
  if st == 'pending' then return 'HANDLE_MISMATCH' end
  return 'HANDLED'
end
`

// deviceFlowBeginApproveScript: KEYS flow; ARGV now ms, uid, csh, hnd, lease
// ms, minimum remaining ms, max attempts, anonce. On success the flow becomes
// approving with a fresh lease and nonce and {OK, att} is returned. This CAS
// runs before any Hydra call, so a user code is approved at most once.
const deviceFlowBeginApproveScript = deviceFlowLazyExpiryLua + deviceFlowDecisionGuardLua + `
if redis.call('EXISTS', KEYS[1]) == 0 then return {'EXPIRED'} end
local now = tonumber(ARGV[1])
local refused = decision_guard(KEYS[1], now, ARGV[2], ARGV[3], ARGV[4])
if refused ~= '' then return {refused} end
local f = redis.call('HMGET', KEYS[1], 'exp', 'att')
if (tonumber(f[1]) or 0) - now < tonumber(ARGV[6]) then return {'TOO_LATE'} end
local att = tonumber(f[2]) or 0
if att >= tonumber(ARGV[7]) then
  redis.call('HSET', KEYS[1], 'st', 'failed')
  return {'MAX_ATTEMPTS'}
end
att = att + 1
redis.call('HSET', KEYS[1], 'st', 'approving', 'auntil', now + tonumber(ARGV[5]), 'anonce', ARGV[8], 'att', att)
return {'OK', tostring(att)}
`

// deviceFlowRecordConsentScript: KEYS flow, unredeemed; ARGV anonce, crid,
// sub, fid. Runs at H9h, before consent accept: from here on Hydra may hold a
// consent session for the flow, so it joins the unredeemed set (score exp).
const deviceFlowRecordConsentScript = `
local f = redis.call('HMGET', KEYS[1], 'st', 'anonce', 'exp')
if f[1] ~= 'approving' or f[2] ~= ARGV[1] then return 'NONCE_MISMATCH' end
redis.call('HSET', KEYS[1], 'crid', ARGV[2], 'sub', ARGV[3])
redis.call('ZADD', KEYS[2], tonumber(f[3]) or 0, ARGV[4])
return 'OK'
`

// deviceFlowFinishApproveScript: KEYS flow, unredeemed; ARGV anonce, mode
// (approved, unconfirmed, revert, failed), fid, label, label source, max
// attempts. It returns {code, state}. A flow the device already redeemed
// answers ALREADY_ISSUED, which callers treat as success. revert returns the
// flow to claimed (failed once the attempts are used up) and leaves the
// unredeemed set; failed keeps the set membership so a recorded consent is
// still revoked by the reaper.
const deviceFlowFinishApproveScript = `
local f = redis.call('HMGET', KEYS[1], 'st', 'anonce', 'att')
if f[1] == 'issued' then return {'ALREADY_ISSUED', 'issued'} end
if f[1] ~= 'approving' or f[2] ~= ARGV[1] then return {'NONCE_MISMATCH', f[1] or ''} end
local mode = ARGV[2]
local st
if mode == 'approved' or mode == 'unconfirmed' then
  st = mode
  redis.call('HSET', KEYS[1], 'lbl', ARGV[4], 'lsrc', ARGV[5])
elseif mode == 'revert' then
  st = 'claimed'
  if (tonumber(f[3]) or 0) >= tonumber(ARGV[6]) then st = 'failed' end
  redis.call('ZREM', KEYS[2], ARGV[3])
else
  st = 'failed'
end
redis.call('HSET', KEYS[1], 'st', st)
redis.call('HDEL', KEYS[1], 'anonce', 'auntil')
return {'OK', st}
`

// deviceFlowDenyScript: KEYS flow; ARGV now ms, uid, csh, hnd, reason. Allowed
// on claimed flows and on approving flows whose lease ran out; the latter may
// already hold a completed consent in Hydra, so the caller revokes the
// returned crid. The unredeemed set is left alone: the flow leaves it once
// that revocation succeeded.
const deviceFlowDenyScript = deviceFlowLazyExpiryLua + deviceFlowDecisionGuardLua + `
if redis.call('EXISTS', KEYS[1]) == 0 then return {'EXPIRED'} end
local now = tonumber(ARGV[1])
local refused = decision_guard(KEYS[1], now, ARGV[2], ARGV[3], ARGV[4])
if refused ~= '' then return {refused} end
redis.call('HSET', KEYS[1], 'st', 'denied', 'dres', ARGV[5])
redis.call('HDEL', KEYS[1], 'anonce', 'auntil')
return {'OK', redis.call('HGET', KEYS[1], 'crid') or ''}
`

// deviceFlowFailScript: KEYS flow; ARGV now ms. Used when the H2 refresh of a
// lookup or approve finds the client unusable: pending and claimed flows
// become failed; any other state is returned unchanged.
const deviceFlowFailScript = deviceFlowLazyExpiryLua + `
if redis.call('EXISTS', KEYS[1]) == 0 then return '' end
local st = lazy_expire(KEYS[1], tonumber(ARGV[1]))
if st == 'pending' or st == 'claimed' then
  redis.call('HSET', KEYS[1], 'st', 'failed')
  return 'failed'
end
return st
`

// deviceClaimResult is the outcome of a lookup's claim.
type deviceClaimResult struct {
	Code        string
	FlowID      string
	ClientID    string
	ClientType  string
	Scope       string
	DeviceLabel string
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// claim runs the claim script for a normalized user code on behalf of a user
// and session, binding the flow to a new flow handle.
func (s *deviceFlowStore) claim(ctx context.Context, normalizedUserCode, flowHandle, userID, sessionHash string, claimTTL time.Duration) (deviceClaimResult, error) {
	manager, err := s.redisManager()
	if err != nil {
		return deviceClaimResult{}, err
	}
	keys := manager.KeyBuilder()
	values, err := manager.Redis.Eval(ctx, deviceFlowClaimScript, []string{
		keys.BuildOAuth2DeviceUserCodeIndexKey(normalizedUserCode),
		keys.BuildOAuth2DeviceFlowHandleIndexKey(flowHandle),
	}, s.nowMillis(), userID, sessionHash, keys.HashOAuth2DeviceIdentifier("fh", flowHandle), claimTTL.Milliseconds(), keys.BuildOAuth2DeviceFlowKey("")).StringSlice()
	if err != nil {
		return deviceClaimResult{}, err
	}
	if len(values) == 0 {
		return deviceClaimResult{}, errors.New("empty device claim result")
	}
	result := deviceClaimResult{Code: values[0]}
	if len(values) > 1 {
		result.FlowID = values[1]
	}
	if result.Code == deviceClaimNew || result.Code == deviceClaimRenewed {
		if len(values) != 8 {
			return deviceClaimResult{}, fmt.Errorf("unexpected device claim result of %d values", len(values))
		}
		created, createdErr := strconv.ParseInt(values[6], 10, 64)
		expires, expiresErr := strconv.ParseInt(values[7], 10, 64)
		if createdErr != nil || expiresErr != nil {
			return deviceClaimResult{}, errors.New("invalid device flow timestamps")
		}
		result.ClientID, result.ClientType, result.Scope, result.DeviceLabel = values[2], values[3], values[4], values[5]
		result.CreatedAt, result.ExpiresAt = time.UnixMilli(created).UTC(), time.UnixMilli(expires).UTC()
	}
	return result, nil
}

// flowIDForHandle resolves a browser flow handle through the fh index.
func (s *deviceFlowStore) flowIDForHandle(ctx context.Context, flowHandle string) (string, bool, error) {
	manager, err := s.redisManager()
	if err != nil {
		return "", false, err
	}
	flowID, err := manager.Redis.Get(ctx, manager.KeyBuilder().BuildOAuth2DeviceFlowHandleIndexKey(flowHandle)).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return flowID, true, nil
}

// deviceFlowSnapshot is the part of a flow the approve handler reads before
// its CAS. Only creation-time fields are used, which never change.
type deviceFlowSnapshot struct {
	ClientID     string
	ClientType   string
	Scope        string
	DeviceLabel  string
	UserCodeHash string
	ExpiresAt    time.Time
}

func (s *deviceFlowStore) snapshot(ctx context.Context, flowID string) (deviceFlowSnapshot, bool, error) {
	manager, err := s.redisManager()
	if err != nil {
		return deviceFlowSnapshot{}, false, err
	}
	values, err := manager.Redis.HMGet(ctx, manager.KeyBuilder().BuildOAuth2DeviceFlowKey(flowID), "cid", "ctype", "scope", "dlb", "uch", "exp").Result()
	if err != nil {
		return deviceFlowSnapshot{}, false, err
	}
	fields := make([]string, len(values))
	for i, value := range values {
		fields[i], _ = value.(string)
	}
	if fields[0] == "" {
		return deviceFlowSnapshot{}, false, nil
	}
	expires, _ := strconv.ParseInt(fields[5], 10, 64)
	return deviceFlowSnapshot{
		ClientID:     fields[0],
		ClientType:   fields[1],
		Scope:        fields[2],
		DeviceLabel:  fields[3],
		UserCodeHash: fields[4],
		ExpiresAt:    time.UnixMilli(expires).UTC(),
	}, true, nil
}

// hashUserCode is hx("uc", normalizedUserCode), the value stored as uch.
func (s *deviceFlowStore) hashUserCode(normalizedUserCode string) (string, error) {
	manager, err := s.redisManager()
	if err != nil {
		return "", err
	}
	return manager.KeyBuilder().HashOAuth2DeviceIdentifier("uc", normalizedUserCode), nil
}

// deviceDecisionActor identifies who decides a flow: the Toolbox user, the
// hashed session the claim was made in, and the flow handle presented.
type deviceDecisionActor struct {
	UserID      string
	SessionHash string
	FlowHandle  string
}

func (s *deviceFlowStore) handleHash(flowHandle string) (string, error) {
	manager, err := s.redisManager()
	if err != nil {
		return "", err
	}
	return manager.KeyBuilder().HashOAuth2DeviceIdentifier("fh", flowHandle), nil
}

// beginApprove runs the BeginApprove CAS. It returns the result code and, on
// OK, the attempt number.
func (s *deviceFlowStore) beginApprove(ctx context.Context, flowID string, actor deviceDecisionActor, timings DeviceFlowTimings, nonce string) (string, int, error) {
	manager, err := s.redisManager()
	if err != nil {
		return "", 0, err
	}
	handleHash, _ := s.handleHash(actor.FlowHandle)
	values, err := manager.Redis.Eval(ctx, deviceFlowBeginApproveScript,
		[]string{manager.KeyBuilder().BuildOAuth2DeviceFlowKey(flowID)},
		s.nowMillis(), actor.UserID, actor.SessionHash, handleHash,
		timings.ApproveLease.Milliseconds(), timings.MinRemainingToApprove.Milliseconds(), timings.MaxApproveAttempts, nonce,
	).StringSlice()
	if err != nil {
		return "", 0, err
	}
	if len(values) == 0 {
		return "", 0, errors.New("empty device begin-approve result")
	}
	if values[0] != deviceDecisionOK {
		return values[0], 0, nil
	}
	if len(values) != 2 {
		return "", 0, fmt.Errorf("unexpected device begin-approve result of %d values", len(values))
	}
	attempt, err := strconv.Atoi(values[1])
	if err != nil {
		return "", 0, fmt.Errorf("invalid approve attempt: %w", err)
	}
	return deviceDecisionOK, attempt, nil
}

// recordConsent stores the consent request ID and subject of the attempt
// holding nonce, and adds the flow to the unredeemed set. It returns false on
// a nonce mismatch.
func (s *deviceFlowStore) recordConsent(ctx context.Context, flowID, nonce, consentRequestID, subject string) (bool, error) {
	manager, err := s.redisManager()
	if err != nil {
		return false, err
	}
	keys := manager.KeyBuilder()
	result, err := manager.Redis.Eval(ctx, deviceFlowRecordConsentScript, []string{
		keys.BuildOAuth2DeviceFlowKey(flowID),
		keys.BuildOAuth2DeviceUnredeemedKey(),
	}, nonce, consentRequestID, subject, flowID).Text()
	if err != nil {
		return false, err
	}
	return result == deviceDecisionOK, nil
}

// finishApprove ends the attempt holding nonce. It returns the result code
// (OK, ALREADY_ISSUED or NONCE_MISMATCH) and the flow's state afterwards.
func (s *deviceFlowStore) finishApprove(ctx context.Context, flowID, nonce, mode, label, labelSource string, maxAttempts int) (string, string, error) {
	manager, err := s.redisManager()
	if err != nil {
		return "", "", err
	}
	keys := manager.KeyBuilder()
	values, err := manager.Redis.Eval(ctx, deviceFlowFinishApproveScript, []string{
		keys.BuildOAuth2DeviceFlowKey(flowID),
		keys.BuildOAuth2DeviceUnredeemedKey(),
	}, nonce, mode, flowID, label, labelSource, maxAttempts).StringSlice()
	if err != nil {
		return "", "", err
	}
	if len(values) != 2 {
		return "", "", fmt.Errorf("unexpected device finish result of %d values", len(values))
	}
	return values[0], values[1], nil
}

// deny records a denial. On OK it also returns the flow's consent request ID
// (possibly empty), which the caller revokes.
func (s *deviceFlowStore) deny(ctx context.Context, flowID string, actor deviceDecisionActor, reason string) (string, string, error) {
	manager, err := s.redisManager()
	if err != nil {
		return "", "", err
	}
	handleHash, _ := s.handleHash(actor.FlowHandle)
	values, err := manager.Redis.Eval(ctx, deviceFlowDenyScript,
		[]string{manager.KeyBuilder().BuildOAuth2DeviceFlowKey(flowID)},
		s.nowMillis(), actor.UserID, actor.SessionHash, handleHash, reason,
	).StringSlice()
	if err != nil {
		return "", "", err
	}
	if len(values) == 0 {
		return "", "", errors.New("empty device deny result")
	}
	if values[0] != deviceDecisionOK {
		return values[0], "", nil
	}
	if len(values) != 2 {
		return "", "", fmt.Errorf("unexpected device deny result of %d values", len(values))
	}
	return deviceDecisionOK, values[1], nil
}

// fail marks a pending or claimed flow failed (the client became unusable)
// and returns the flow's state afterwards.
func (s *deviceFlowStore) fail(ctx context.Context, flowID string) (string, error) {
	manager, err := s.redisManager()
	if err != nil {
		return "", err
	}
	return manager.Redis.Eval(ctx, deviceFlowFailScript, []string{manager.KeyBuilder().BuildOAuth2DeviceFlowKey(flowID)}, s.nowMillis()).Text()
}

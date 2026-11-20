package upload

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsoncodec"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
)

var errBirthdayMonitorChanged = errors.New("birthday monitor is no longer current")

const birthdayMirrorLua = birthdayReleaseLua + `
local function read(key)
    local value = redis.call('GET', key)
    if value then return cjson.decode(value) end
    return nil
end
local function cleanup(monitor)
    -- The ledger is bounded by the global pending-event quota. Entries from
    -- before the ledger was introduced expire with their original TTL.
    local prefix = ARGV[4] .. monitor.subscription_id .. ':' .. monitor.subscription_version .. ':'
    local ledger = redis.call('HGETALL', KEYS[2])
    for i = 1, #ledger, 2 do
        local event, entry = ledger[i], cjson.decode(ledger[i + 1])
        if entry[1] == monitor.subscription_id and string.sub(event, 1, string.len(prefix)) == prefix then
            release(event)
        end
    end
end
local index, sub, version = read(KEYS[4]), ARGV[2], ARGV[3]
local previous = index and index.monitor_key and read(index.monitor_key)
local ownsPrevious = previous and previous.subscription_id == sub
if ARGV[1] == 'delete' then
    if not ownsPrevious or previous.subscription_version ~= version then return 0 end
    cleanup(previous)
    redis.call('DEL', KEYS[4], index.monitor_key)
    return 1
end
if ownsPrevious and previous.subscription_version == version and index.monitor_key ~= KEYS[5] then
    return 0
end
local replaced = read(KEYS[5])
if replaced and (replaced.subscription_id ~= sub or replaced.subscription_version ~= version) then
    cleanup(replaced)
    local replacedIndexKey = ARGV[5] .. replaced.subscription_id
    local replacedIndex = read(replacedIndexKey)
    if replacedIndex and replacedIndex.monitor_key == KEYS[5] then
        redis.call('DEL', replacedIndexKey)
    end
end
if ownsPrevious and index.monitor_key ~= KEYS[5] then
    cleanup(previous)
    redis.call('DEL', index.monitor_key)
end
redis.call('SET', KEYS[5], ARGV[6], 'PX', ARGV[8])
redis.call('SET', KEYS[4], ARGV[7], 'PX', ARGV[8])
return 1
`

// These components form part of Redis keys and must stay unambiguous across
// monitor, index and event records.
func validBirthdayKeyPart(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for i := range len(value) {
		c := value[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func birthdayMirrorArgs(mode, subscriptionID, version string) []any {
	return []any{mode, subscriptionID, version,
		harukiRedis.MysekaiBirthdayEventPrefix(),
		harukiRedis.BuildMysekaiBirthdaySubscriptionKey("")}
}

func UpsertBirthdayMonitorMirror(ctx context.Context, redisManager *harukiRedis.HarukiRedisManager, monitor BirthdayMonitorMirror) error {
	if redisManager == nil || redisManager.Redis == nil {
		return fmt.Errorf("redis client is nil")
	}
	monitor.SubscriptionID = strings.TrimSpace(monitor.SubscriptionID)
	monitor.SubscriptionVersion = strings.TrimSpace(monitor.SubscriptionVersion)
	monitor.Region = strings.TrimSpace(monitor.Region)
	monitor.UID = strings.TrimSpace(monitor.UID)
	if !validBirthdayKeyPart(monitor.SubscriptionID) || !validBirthdayKeyPart(monitor.SubscriptionVersion) || !validBirthdayKeyPart(monitor.Region) || !validBirthdayKeyPart(monitor.UID) {
		return fmt.Errorf("invalid subscription_id, subscription_version, region or uid")
	}
	if len(monitor.MaterialIDs) == 0 {
		monitor.MaterialIDs = materialIDsFromNames(monitor.Materials)
	}
	if len(monitor.MaterialIDs) == 0 {
		return fmt.Errorf("material_ids are required")
	}
	ttl := birthdayTTLUntil(monitor.ExpiresAt)
	if ttl <= 0 {
		return fmt.Errorf("expires_at must be in the future")
	}

	monitorKey := harukiRedis.BuildMysekaiBirthdayMonitorKey(monitor.Region, monitor.UID)
	index := BirthdayMonitorSubscriptionIndex{
		SubscriptionID: monitor.SubscriptionID,
		MonitorKey:     monitorKey,
		Region:         monitor.Region,
		UID:            monitor.UID,
	}
	monitorData, err := jsoncodec.Marshal(monitor)
	if err != nil {
		return err
	}
	indexData, err := jsoncodec.Marshal(index)
	if err != nil {
		return err
	}
	keys := append(append([]string(nil), birthdayPendingKeys...), harukiRedis.BuildMysekaiBirthdaySubscriptionKey(monitor.SubscriptionID), monitorKey)
	args := append(birthdayMirrorArgs("upsert", monitor.SubscriptionID, monitor.SubscriptionVersion), monitorData, indexData, ttl.Milliseconds())
	updated, err := redisManager.Redis.Eval(ctx, birthdayMirrorLua, keys, args...).Int()
	if err != nil {
		return err
	}
	if updated == 0 {
		return errBirthdayMonitorChanged
	}
	return nil
}

func GetBirthdayMonitorMirror(ctx context.Context, redisManager *harukiRedis.HarukiRedisManager, region string, uid string) (*BirthdayMonitorMirror, bool, error) {
	if redisManager == nil || redisManager.Redis == nil {
		return nil, false, fmt.Errorf("redis client is nil")
	}
	var monitor BirthdayMonitorMirror
	found, err := redisManager.GetCache(ctx, harukiRedis.BuildMysekaiBirthdayMonitorKey(region, uid), &monitor)
	if err != nil || !found {
		return nil, found, err
	}
	return &monitor, true, nil
}

func DeleteBirthdayMonitorMirror(ctx context.Context, redisManager *harukiRedis.HarukiRedisManager, subscriptionID string, subscriptionVersion string) error {
	if redisManager == nil || redisManager.Redis == nil {
		return fmt.Errorf("redis client is nil")
	}
	subscriptionID = strings.TrimSpace(subscriptionID)
	subscriptionVersion = strings.TrimSpace(subscriptionVersion)
	if !validBirthdayKeyPart(subscriptionID) || !validBirthdayKeyPart(subscriptionVersion) {
		return fmt.Errorf("invalid subscription_id or subscription_version")
	}
	keys := append(append([]string(nil), birthdayPendingKeys...), harukiRedis.BuildMysekaiBirthdaySubscriptionKey(subscriptionID))
	return redisManager.Redis.Eval(ctx, birthdayMirrorLua, keys, birthdayMirrorArgs("delete", subscriptionID, subscriptionVersion)...).Err()
}

func StoreBirthdayMonitorEvent(ctx context.Context, redisManager *harukiRedis.HarukiRedisManager, monitor *BirthdayMonitorMirror, event BirthdayMonitorEvent) (*BirthdayMonitorEvent, error) {
	if redisManager == nil || redisManager.Redis == nil {
		return nil, fmt.Errorf("redis client is nil")
	}
	if monitor == nil {
		return nil, fmt.Errorf("monitor is nil")
	}
	event.EventID = strings.TrimSpace(event.EventID)
	if event.EventID == "" {
		event.EventID = newBirthdayEventID()
	}
	event.SubscriptionID = strings.TrimSpace(monitor.SubscriptionID)
	event.SubscriptionVersion = strings.TrimSpace(monitor.SubscriptionVersion)
	event.Region = strings.TrimSpace(event.Region)
	if event.Region == "" {
		event.Region = strings.TrimSpace(monitor.Region)
	}
	event.UID = strings.TrimSpace(event.UID)
	if event.UID == "" {
		event.UID = strings.TrimSpace(monitor.UID)
	}
	event.CreatedAt = time.Now().Unix()
	event.PayloadRef = harukiRedis.BuildMysekaiBirthdayEventKey(event.SubscriptionID, event.SubscriptionVersion, event.EventID)
	if !validBirthdayKeyPart(event.SubscriptionID) || !validBirthdayKeyPart(event.SubscriptionVersion) || !validBirthdayKeyPart(event.EventID) || !validBirthdayKeyPart(event.Region) || !validBirthdayKeyPart(event.UID) {
		return nil, fmt.Errorf("event identity is incomplete")
	}
	ttl := birthdayTTLUntil(monitor.ExpiresAt)
	if ttl <= 0 {
		return nil, fmt.Errorf("monitor has expired")
	}
	return storePendingBirthdayEvent(ctx, redisManager.Redis, event, ttl)
}

func FetchBirthdayMonitorEvent(ctx context.Context, redisManager *harukiRedis.HarukiRedisManager, eventID string, subscriptionID string, subscriptionVersion string) (*BirthdayMonitorEvent, error) {
	if redisManager == nil || redisManager.Redis == nil {
		return nil, fmt.Errorf("redis client is nil")
	}
	eventID = strings.TrimSpace(eventID)
	subscriptionID = strings.TrimSpace(subscriptionID)
	subscriptionVersion = strings.TrimSpace(subscriptionVersion)
	if !validBirthdayKeyPart(eventID) || !validBirthdayKeyPart(subscriptionID) || !validBirthdayKeyPart(subscriptionVersion) {
		return nil, fmt.Errorf("event_id, subscription_id and subscription_version are required")
	}
	var event BirthdayMonitorEvent
	found, err := redisManager.GetCache(ctx, harukiRedis.BuildMysekaiBirthdayEventKey(subscriptionID, subscriptionVersion, eventID), &event)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("birthday monitor event not found")
	}
	monitor, active, err := GetBirthdayMonitorMirror(ctx, redisManager, event.Region, event.UID)
	if err != nil {
		return nil, err
	}
	if !active || monitor.SubscriptionID != subscriptionID || monitor.SubscriptionVersion != subscriptionVersion {
		return nil, errBirthdayMonitorChanged
	}
	return &event, nil
}

func AckBirthdayMonitorEvent(ctx context.Context, redisManager *harukiRedis.HarukiRedisManager, eventID string, subscriptionID string, subscriptionVersion string) error {
	if redisManager == nil || redisManager.Redis == nil {
		return fmt.Errorf("redis client is nil")
	}
	eventID = strings.TrimSpace(eventID)
	subscriptionID = strings.TrimSpace(subscriptionID)
	subscriptionVersion = strings.TrimSpace(subscriptionVersion)
	if !validBirthdayKeyPart(eventID) || !validBirthdayKeyPart(subscriptionID) || !validBirthdayKeyPart(subscriptionVersion) {
		return fmt.Errorf("event_id, subscription_id and subscription_version are required")
	}
	return deletePendingBirthdayEvents(ctx, redisManager.Redis, harukiRedis.BuildMysekaiBirthdayEventKey(subscriptionID, subscriptionVersion, eventID))
}

func birthdayTTLUntil(expiresAt int64) time.Duration {
	if expiresAt <= 0 {
		return 0
	}
	ttl := time.Until(time.Unix(expiresAt, 0))
	if ttl <= 0 {
		return 0
	}
	return ttl + 10*time.Minute
}

func newBirthdayEventID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.FormatUint(rand.Uint64(), 36)
}

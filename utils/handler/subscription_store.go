package handler

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"

	goredis "github.com/redis/go-redis/v9"
)

func UpsertBirthdayMonitorMirror(ctx context.Context, redisManager *harukiRedis.HarukiRedisManager, monitor BirthdayMonitorMirror) error {
	if redisManager == nil || redisManager.Redis == nil {
		return fmt.Errorf("redis client is nil")
	}
	monitor.SubscriptionID = strings.TrimSpace(monitor.SubscriptionID)
	monitor.SubscriptionVersion = strings.TrimSpace(monitor.SubscriptionVersion)
	monitor.Region = strings.TrimSpace(monitor.Region)
	monitor.UID = strings.TrimSpace(monitor.UID)
	if monitor.SubscriptionID == "" || monitor.SubscriptionVersion == "" || monitor.Region == "" || monitor.UID == "" {
		return fmt.Errorf("subscription_id, subscription_version, region and uid are required")
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

	if err := deleteBirthdayEventsBySubscription(ctx, redisManager.Redis, monitor.SubscriptionID); err != nil {
		return err
	}

	monitorKey := harukiRedis.BuildMysekaiBirthdayMonitorKey(monitor.Region, monitor.UID)
	index := BirthdayMonitorSubscriptionIndex{
		SubscriptionID: monitor.SubscriptionID,
		MonitorKey:     monitorKey,
		Region:         monitor.Region,
		UID:            monitor.UID,
	}
	return redisManager.SetCachesAtomically(ctx, []harukiRedis.CacheItem{
		{Key: monitorKey, Value: monitor},
		{Key: harukiRedis.BuildMysekaiBirthdaySubscriptionKey(monitor.SubscriptionID), Value: index},
	}, ttl)
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
	if subscriptionID == "" {
		return fmt.Errorf("subscription_id is required")
	}

	indexKey := harukiRedis.BuildMysekaiBirthdaySubscriptionKey(subscriptionID)
	var index BirthdayMonitorSubscriptionIndex
	found, err := redisManager.GetCache(ctx, indexKey, &index)
	if err != nil {
		return err
	}
	keys := []string{indexKey}
	if found && strings.TrimSpace(index.MonitorKey) != "" {
		if subscriptionVersion != "" {
			var monitor BirthdayMonitorMirror
			monitorFound, getErr := redisManager.GetCache(ctx, index.MonitorKey, &monitor)
			if getErr != nil {
				return getErr
			}
			if monitorFound && monitor.SubscriptionVersion != subscriptionVersion {
				return fmt.Errorf("subscription_version mismatch")
			}
		}
		keys = append(keys, index.MonitorKey)
	}
	if err := deleteBirthdayEventsBySubscription(ctx, redisManager.Redis, subscriptionID); err != nil {
		return err
	}
	return redisManager.Redis.Del(ctx, keys...).Err()
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
	if event.SubscriptionID == "" || event.SubscriptionVersion == "" || event.EventID == "" {
		return nil, fmt.Errorf("event identity is incomplete")
	}
	ttl := birthdayTTLUntil(monitor.ExpiresAt)
	if ttl <= 0 {
		return nil, fmt.Errorf("monitor has expired")
	}
	if err := redisManager.SetCache(ctx, event.PayloadRef, event, ttl); err != nil {
		return nil, err
	}
	return &event, nil
}

func FetchBirthdayMonitorEvent(ctx context.Context, redisManager *harukiRedis.HarukiRedisManager, eventID string, subscriptionID string, subscriptionVersion string) (*BirthdayMonitorEvent, error) {
	if redisManager == nil || redisManager.Redis == nil {
		return nil, fmt.Errorf("redis client is nil")
	}
	eventID = strings.TrimSpace(eventID)
	subscriptionID = strings.TrimSpace(subscriptionID)
	subscriptionVersion = strings.TrimSpace(subscriptionVersion)
	if eventID == "" || subscriptionID == "" || subscriptionVersion == "" {
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
	return &event, nil
}

func AckBirthdayMonitorEvent(ctx context.Context, redisManager *harukiRedis.HarukiRedisManager, eventID string, subscriptionID string, subscriptionVersion string) error {
	if redisManager == nil || redisManager.Redis == nil {
		return fmt.Errorf("redis client is nil")
	}
	eventID = strings.TrimSpace(eventID)
	subscriptionID = strings.TrimSpace(subscriptionID)
	subscriptionVersion = strings.TrimSpace(subscriptionVersion)
	if eventID == "" || subscriptionID == "" || subscriptionVersion == "" {
		return fmt.Errorf("event_id, subscription_id and subscription_version are required")
	}
	return redisManager.DeleteCache(ctx, harukiRedis.BuildMysekaiBirthdayEventKey(subscriptionID, subscriptionVersion, eventID))
}

func deleteBirthdayEventsBySubscription(ctx context.Context, redisClient *goredis.Client, subscriptionID string) error {
	if redisClient == nil {
		return fmt.Errorf("redis client is nil")
	}
	pattern := harukiRedis.BuildMysekaiBirthdaySubscriptionEventsPattern(subscriptionID)
	var cursor uint64
	for {
		keys, nextCursor, err := redisClient.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return fmt.Errorf("scan birthday monitor events: %w", err)
		}
		if len(keys) > 0 {
			if err := redisClient.Del(ctx, keys...).Err(); err != nil {
				return fmt.Errorf("delete birthday monitor events: %w", err)
			}
		}
		if nextCursor == 0 {
			return nil
		}
		cursor = nextCursor
	}
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

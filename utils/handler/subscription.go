package handler

import (
	"context"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	"strconv"
	"time"
)

func (h *DataHandler) ProcessBirthdaySubscriptionAsync(userID int64, server utils.SupportedDataUploadServer, data map[string]any) {
	go h.processBirthdaySubscription(userID, server, data)
}

func (h *DataHandler) processBirthdaySubscription(userID int64, server utils.SupportedDataUploadServer, data map[string]any) {
	cfg := config.Cfg.Subscription
	redisManager := h.birthdayRedis()
	if redisManager == nil {
		h.Logger.Warnf("birthday subscription skipped: redis is unavailable server=%s uid=%d", server, userID)
		return
	}
	timeout := time.Duration(cfg.RequestTimeoutSecond) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	monitor, found, err := GetBirthdayMonitorMirror(ctx, redisManager, string(server), strconv.FormatInt(userID, 10))
	if err != nil {
		h.Logger.Warnf("birthday subscription monitor lookup failed: server=%s uid=%d err=%v", server, userID, err)
		return
	}
	if !found || monitor == nil || monitor.SubscriptionID == "" {
		h.Logger.Debugf("birthday subscription skipped: monitor not found server=%s uid=%d", server, userID)
		return
	}
	if monitor.ExpiresAt > 0 && time.Now().Unix() >= monitor.ExpiresAt {
		_ = DeleteBirthdayMonitorMirror(ctx, redisManager, monitor.SubscriptionID, monitor.SubscriptionVersion)
		h.Logger.Infof("birthday subscription expired and cleaned: server=%s uid=%d subscription=%s version=%s", server, userID, monitor.SubscriptionID, monitor.SubscriptionVersion)
		return
	}

	h.Logger.Infof("birthday subscription monitor matched: server=%s uid=%d subscription=%s version=%s materials=%v notify_empty=%t", server, userID, monitor.SubscriptionID, monitor.SubscriptionVersion, monitor.MaterialIDs, monitor.NotifyEmpty)
	filtered, matchedMaterialIDs, emptyResult := FilterBirthdayPartyPayload(data, monitor.MaterialIDs)
	if emptyResult && !monitor.NotifyEmpty {
		h.Logger.Infof("birthday subscription update skipped: empty result server=%s uid=%d subscription=%s version=%s", server, userID, monitor.SubscriptionID, monitor.SubscriptionVersion)
		return
	}
	event, err := StoreBirthdayMonitorEvent(ctx, redisManager, monitor, BirthdayMonitorEvent{
		Region:             string(server),
		UID:                strconv.FormatInt(userID, 10),
		UploadTime:         int64FromAny(data["upload_time"]),
		MatchedMaterialIDs: matchedMaterialIDs,
		EmptyResult:        emptyResult,
		FilteredPayload:    filtered,
	})
	if err != nil {
		h.Logger.Warnf("birthday subscription event store failed: server=%s uid=%d subscription=%s err=%v", server, userID, monitor.SubscriptionID, err)
		return
	}
	h.Logger.Infof("birthday subscription event stored: event=%s subscription=%s version=%s empty_result=%t matched_material_ids=%v", event.EventID, event.SubscriptionID, event.SubscriptionVersion, event.EmptyResult, event.MatchedMaterialIDs)
	if err := h.notifyHMESBirthdayEvent(ctx, event); err != nil {
		h.Logger.Warnf("birthday subscription HMES notify failed: event=%s subscription=%s err=%v", event.EventID, event.SubscriptionID, err)
	}
}

func (h *DataHandler) birthdayRedis() *harukiRedis.HarukiRedisManager {
	if h == nil || h.DBManager == nil || h.DBManager.Redis == nil || h.DBManager.Redis.Redis == nil {
		return nil
	}
	return h.DBManager.Redis
}

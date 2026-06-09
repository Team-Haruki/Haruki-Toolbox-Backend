package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"haruki-suite/config"
	"haruki-suite/utils"
	harukiRedis "haruki-suite/utils/database/redis"
	"math"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
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

func FilterBirthdayPartyPayload(data map[string]any, materialIDs []int) (map[string]any, []int, bool) {
	targets := make(map[int]struct{}, len(materialIDs))
	for _, id := range materialIDs {
		if id > 0 {
			targets[id] = struct{}{}
		}
	}

	filteredMaps := make([]any, 0)
	matchedIDs := make([]int, 0, len(targets))
	matchedSet := make(map[int]struct{}, len(targets))
	for _, rawMap := range birthdayHarvestMaps(data) {
		siteMap, ok := mapStringAny(rawMap)
		if !ok {
			continue
		}
		drops := anySlice(siteMap["userMysekaiSiteHarvestResourceDrops"])
		fixtures := anySlice(siteMap["userMysekaiSiteHarvestFixtures"])

		fixtureIDsByPosition := make(map[string]map[int]struct{})
		positionsByFixtureID := make(map[int]map[string]struct{})
		for _, rawFixture := range fixtures {
			fixture, ok := mapStringAny(rawFixture)
			if !ok {
				continue
			}
			fixtureID := birthdayFixtureID(fixture)
			if fixtureID <= 0 {
				continue
			}
			posKey := birthdayPosKey(fixture)
			if posKey == "" {
				continue
			}
			if fixtureIDsByPosition[posKey] == nil {
				fixtureIDsByPosition[posKey] = make(map[int]struct{})
			}
			fixtureIDsByPosition[posKey][fixtureID] = struct{}{}
			if positionsByFixtureID[fixtureID] == nil {
				positionsByFixtureID[fixtureID] = make(map[string]struct{})
			}
			positionsByFixtureID[fixtureID][posKey] = struct{}{}
		}

		matchedPositions := make(map[string]struct{})
		matchedFixtureIDs := make(map[int]struct{})
		siteMatched := false
		for _, rawDrop := range drops {
			drop, ok := mapStringAny(rawDrop)
			if !ok {
				continue
			}
			resourceType := normalizeBirthdayResourceType(stringFromAny(firstPresent(drop, "resourceType", "type")))
			resourceID := intFromAny(firstPresent(drop, "resourceId", "id"))
			if resourceType != "mysekai_material" {
				continue
			}
			if _, ok := targets[resourceID]; !ok {
				continue
			}
			siteMatched = true
			if _, ok := matchedSet[resourceID]; !ok {
				matchedSet[resourceID] = struct{}{}
				matchedIDs = append(matchedIDs, resourceID)
			}
			if key := birthdayPosKey(drop); key != "" {
				matchedPositions[key] = struct{}{}
				for fixtureID := range fixtureIDsByPosition[key] {
					matchedFixtureIDs[fixtureID] = struct{}{}
				}
			}
			if fixtureID := birthdayFixtureID(drop); fixtureID > 0 {
				matchedFixtureIDs[fixtureID] = struct{}{}
			}
		}
		if !siteMatched {
			continue
		}

		keptPositions := make(map[string]struct{}, len(matchedPositions))
		for posKey := range matchedPositions {
			keptPositions[posKey] = struct{}{}
		}
		for fixtureID := range matchedFixtureIDs {
			for posKey := range positionsByFixtureID[fixtureID] {
				keptPositions[posKey] = struct{}{}
			}
		}

		keptDrops := make([]any, 0)
		for _, rawDrop := range drops {
			drop, ok := mapStringAny(rawDrop)
			if !ok {
				continue
			}
			keep := false
			resourceType := normalizeBirthdayResourceType(stringFromAny(firstPresent(drop, "resourceType", "type")))
			resourceID := intFromAny(firstPresent(drop, "resourceId", "id"))
			if resourceType == "mysekai_material" {
				_, keep = targets[resourceID]
			}
			if !keep {
				if _, ok := keptPositions[birthdayPosKey(drop)]; ok {
					keep = true
				}
			}
			if !keep {
				if fixtureID := birthdayFixtureID(drop); fixtureID > 0 {
					_, keep = matchedFixtureIDs[fixtureID]
				}
			}
			if keep {
				keptDrops = append(keptDrops, cloneMap(drop))
			}
		}

		keptFixtures := make([]any, 0)
		for _, rawFixture := range fixtures {
			fixture, ok := mapStringAny(rawFixture)
			if !ok {
				continue
			}
			if _, ok := keptPositions[birthdayPosKey(fixture)]; ok {
				keptFixtures = append(keptFixtures, cloneMap(fixture))
				continue
			}
			if fixtureID := birthdayFixtureID(fixture); fixtureID > 0 {
				if _, ok := matchedFixtureIDs[fixtureID]; ok {
					keptFixtures = append(keptFixtures, cloneMap(fixture))
				}
			}
		}

		copiedMap := cloneMap(siteMap)
		copiedMap["userMysekaiSiteHarvestResourceDrops"] = keptDrops
		copiedMap["userMysekaiSiteHarvestFixtures"] = keptFixtures
		filteredMaps = append(filteredMaps, copiedMap)
	}

	slices.Sort(matchedIDs)
	filtered := map[string]any{
		"updatedResources": map[string]any{
			"userMysekaiHarvestMaps": filteredMaps,
		},
		"upload_time": data["upload_time"],
		"server":      data["server"],
		"source":      "toolbox_live",
	}
	return filtered, matchedIDs, len(matchedIDs) == 0
}

func materialIDsFromNames(materials []string) []int {
	ids := make([]int, 0, len(materials))
	seen := make(map[int]struct{}, len(materials))
	for _, name := range materials {
		id := 0
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "diamond", "mysekai_material_12":
			id = 12
		case "yuugiri", "yugiri", "mysekai_material_5":
			id = 5
		case "clover", "mysekai_material_20":
			id = 20
		}
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
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

func birthdayHarvestMaps(data map[string]any) []any {
	updated, ok := mapStringAny(data["updatedResources"])
	if !ok {
		return nil
	}
	return anySlice(updated["userMysekaiHarvestMaps"])
}

func firstPresent(item map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := item[key]; ok {
			return value
		}
	}
	return nil
}

func mapStringAny(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case map[any]any:
		result := make(map[string]any, len(typed))
		for key, value := range typed {
			switch keyText := key.(type) {
			case string:
				result[keyText] = value
			case []byte:
				result[string(keyText)] = value
			}
		}
		return result, len(result) > 0
	default:
		return nil, false
	}
}

func anySlice(value any) []any {
	switch typed := value.(type) {
	case []any:
		return typed
	case []map[string]any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, item)
		}
		return result
	case []map[any]any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, item)
		}
		return result
	default:
		return nil
	}
}

func cloneMap(item map[string]any) map[string]any {
	result := make(map[string]any, len(item))
	for key, value := range item {
		result[key] = value
	}
	return result
}

func normalizeBirthdayResourceType(resourceType string) string {
	switch strings.ToLower(strings.TrimSpace(resourceType)) {
	case "mysekai_material":
		return "mysekai_material"
	case "material":
		return "material"
	case "item", "mysekai_item":
		return "mysekai_item"
	case "fixture", "mysekai_fixture":
		return "mysekai_fixture"
	case "music_record", "mysekai_music_record":
		return "mysekai_music_record"
	default:
		return strings.TrimSpace(resourceType)
	}
}

func birthdayPosKey(item map[string]any) string {
	xRaw := firstPresent(item, "positionX", "position_x")
	zRaw := firstPresent(item, "positionZ", "position_z")
	if xRaw == nil || zRaw == nil {
		return ""
	}
	x := floatFromAny(xRaw)
	z := floatFromAny(zRaw)
	return fmt.Sprintf("%.3f_%.3f", x, z)
}

func birthdayFixtureID(item map[string]any) int {
	return intFromAny(firstPresent(
		item,
		"mysekaiSiteHarvestFixtureId",
		"mysekaiSiteHarvestFixtureID",
		"mysekai_site_harvest_fixture_id",
		"mysekaiFixtureId",
		"mysekaiFixtureID",
		"mysekai_fixture_id",
	))
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []byte:
		return strings.TrimSpace(string(typed))
	default:
		return fmt.Sprint(value)
	}
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int8:
		return int(typed)
	case int16:
		return int(typed)
	case int32:
		return int(typed)
	case int64:
		if typed > int64(math.MaxInt) || typed < int64(math.MinInt) {
			return 0
		}
		return int(typed)
	case uint:
		if uint64(typed) > uint64(math.MaxInt) {
			return 0
		}
		return int(typed)
	case uint8:
		return int(typed)
	case uint16:
		return int(typed)
	case uint32:
		if uint64(typed) > uint64(math.MaxInt) {
			return 0
		}
		return int(typed)
	case uint64:
		if typed > uint64(math.MaxInt) {
			return 0
		}
		return int(typed)
	case float32:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		if n, err := typed.Int64(); err == nil {
			if n > int64(math.MaxInt) || n < int64(math.MinInt) {
				return 0
			}
			return int(n)
		}
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(typed))
		return n
	case []byte:
		n, _ := strconv.Atoi(strings.TrimSpace(string(typed)))
		return n
	}
	return 0
}

func int64FromAny(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		if n, err := typed.Int64(); err == nil {
			return n
		}
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return n
	}
	return time.Now().Unix()
}

func floatFromAny(value any) float64 {
	switch typed := value.(type) {
	case int:
		return float64(typed)
	case int8:
		return float64(typed)
	case int16:
		return float64(typed)
	case int32:
		return float64(typed)
	case int64:
		return float64(typed)
	case uint:
		return float64(typed)
	case uint8:
		return float64(typed)
	case uint16:
		return float64(typed)
	case uint32:
		return float64(typed)
	case uint64:
		return float64(typed)
	case float32:
		return float64(typed)
	case float64:
		return typed
	case json.Number:
		n, _ := typed.Float64()
		return n
	case string:
		n, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return n
	case []byte:
		n, _ := strconv.ParseFloat(strings.TrimSpace(string(typed)), 64)
		return n
	}
	return 0
}

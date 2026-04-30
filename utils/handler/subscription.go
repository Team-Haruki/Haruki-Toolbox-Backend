package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"haruki-suite/config"
	"haruki-suite/utils"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

type activeBirthdaySubscriptionResponse struct {
	Active         bool     `json:"active"`
	SubscriptionID string   `json:"subscription_id"`
	Materials      []string `json:"materials"`
	MaterialIDs    []int    `json:"material_ids"`
	NotifyEmpty    bool     `json:"notify_empty"`
	Data           *struct {
		Active         bool     `json:"active"`
		SubscriptionID string   `json:"subscription_id"`
		Materials      []string `json:"materials"`
		MaterialIDs    []int    `json:"material_ids"`
		NotifyEmpty    bool     `json:"notify_empty"`
	} `json:"data,omitempty"`
}

type birthdayEventWriteRequest struct {
	SubscriptionID     string         `json:"subscription_id"`
	Region             string         `json:"region"`
	UID                string         `json:"uid"`
	UploadTime         int64          `json:"upload_time"`
	MatchedMaterialIDs []int          `json:"matched_material_ids"`
	EmptyResult        bool           `json:"empty_result"`
	FilteredPayload    map[string]any `json:"filtered_payload"`
}

type birthdayEventWriteResponse struct {
	EventID        string `json:"event_id"`
	SubscriptionID string `json:"subscription_id"`
	EmptyResult    bool   `json:"empty_result"`
}

type hmesEventNotifyRequest struct {
	EventID        string `json:"event_id"`
	SubscriptionID string `json:"subscription_id"`
	EmptyResult    bool   `json:"empty_result"`
}

func (h *DataHandler) ProcessBirthdaySubscriptionAsync(userID int64, server utils.SupportedDataUploadServer, data map[string]any) {
	go h.processBirthdaySubscription(userID, server, data)
}

func (h *DataHandler) processBirthdaySubscription(userID int64, server utils.SupportedDataUploadServer, data map[string]any) {
	cfg := config.Cfg.Subscription
	if strings.TrimSpace(cfg.CloudInternalBaseURL) == "" {
		return
	}
	timeout := time.Duration(cfg.RequestTimeoutSecond) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	active, err := h.queryActiveBirthdaySubscription(ctx, string(server), userID)
	if err != nil {
		h.Logger.Warnf("birthday subscription active query failed: server=%s uid=%d err=%v", server, userID, err)
		return
	}
	if active == nil || !active.Active || active.SubscriptionID == "" {
		return
	}

	filtered, matchedMaterialIDs, emptyResult := FilterBirthdayPartyPayload(data, active.MaterialIDs)
	event, err := h.writeBirthdaySubscriptionEvent(ctx, birthdayEventWriteRequest{
		SubscriptionID:     active.SubscriptionID,
		Region:             string(server),
		UID:                strconv.FormatInt(userID, 10),
		UploadTime:         int64FromAny(data["upload_time"]),
		MatchedMaterialIDs: matchedMaterialIDs,
		EmptyResult:        emptyResult,
		FilteredPayload:    filtered,
	})
	if err != nil {
		h.Logger.Warnf("birthday subscription event write failed: server=%s uid=%d subscription=%s err=%v", server, userID, active.SubscriptionID, err)
		return
	}
	if err := h.notifyHMESBirthdayEvent(ctx, event); err != nil {
		h.Logger.Warnf("birthday subscription HMES notify failed: event=%s subscription=%s err=%v", event.EventID, event.SubscriptionID, err)
	}
}

func (h *DataHandler) queryActiveBirthdaySubscription(ctx context.Context, server string, userID int64) (*activeBirthdaySubscriptionResponse, error) {
	cfg := config.Cfg.Subscription
	u, err := url.Parse(strings.TrimRight(cfg.CloudInternalBaseURL, "/") + "/internal/subscriptions/mysekai-birthday/active")
	if err != nil {
		return nil, err
	}
	query := u.Query()
	query.Set("region", server)
	query.Set("uid", strconv.FormatInt(userID, 10))
	u.RawQuery = query.Encode()

	status, _, body, err := h.HttpClient.Request(ctx, "GET", u.String(), subscriptionHeaders(cfg.CloudInternalToken, cfg.UserAgent), nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("cloud returned status %d: %s", status, strings.TrimSpace(string(body)))
	}

	var resp activeBirthdaySubscriptionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	if resp.Data != nil {
		resp.Active = resp.Data.Active
		resp.SubscriptionID = resp.Data.SubscriptionID
		resp.Materials = resp.Data.Materials
		resp.MaterialIDs = resp.Data.MaterialIDs
		resp.NotifyEmpty = resp.Data.NotifyEmpty
	}
	return &resp, nil
}

func (h *DataHandler) writeBirthdaySubscriptionEvent(ctx context.Context, req birthdayEventWriteRequest) (*birthdayEventWriteResponse, error) {
	cfg := config.Cfg.Subscription
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(cfg.CloudInternalBaseURL, "/") + "/internal/subscription-events/mysekai-birthday"
	status, _, respBody, err := h.HttpClient.Request(ctx, "POST", endpoint, subscriptionJSONHeaders(cfg.CloudInternalToken, cfg.UserAgent), body)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("cloud returned status %d: %s", status, strings.TrimSpace(string(respBody)))
	}
	var resp birthdayEventWriteResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, err
	}
	if resp.EventID == "" {
		return nil, fmt.Errorf("cloud response missing event_id")
	}
	return &resp, nil
}

func (h *DataHandler) notifyHMESBirthdayEvent(ctx context.Context, event *birthdayEventWriteResponse) error {
	cfg := config.Cfg.Subscription
	if event == nil || strings.TrimSpace(cfg.HMESInternalBaseURL) == "" {
		return nil
	}
	body, err := json.Marshal(hmesEventNotifyRequest{
		EventID:        event.EventID,
		SubscriptionID: event.SubscriptionID,
		EmptyResult:    event.EmptyResult,
	})
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(cfg.HMESInternalBaseURL, "/") + "/internal/events"
	status, _, respBody, err := h.HttpClient.Request(ctx, "POST", endpoint, subscriptionJSONHeaders(cfg.HMESInternalToken, cfg.UserAgent), body)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("hmes returned status %d: %s", status, strings.TrimSpace(string(respBody)))
	}
	return nil
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

		keptDrops := make([]any, 0)
		matchedPositions := make(map[string]struct{})
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
			keptDrops = append(keptDrops, cloneMap(drop))
			if _, ok := matchedSet[resourceID]; !ok {
				matchedSet[resourceID] = struct{}{}
				matchedIDs = append(matchedIDs, resourceID)
			}
			if key := birthdayPosKey(drop); key != "" {
				matchedPositions[key] = struct{}{}
			}
		}
		if len(keptDrops) == 0 {
			continue
		}

		keptFixtures := make([]any, 0)
		for _, rawFixture := range fixtures {
			fixture, ok := mapStringAny(rawFixture)
			if !ok {
				continue
			}
			if _, ok := matchedPositions[birthdayPosKey(fixture)]; ok {
				keptFixtures = append(keptFixtures, cloneMap(fixture))
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

func birthdayHarvestMaps(data map[string]any) []any {
	updated, ok := mapStringAny(data["updatedResources"])
	if !ok {
		return nil
	}
	return anySlice(updated["userMysekaiHarvestMaps"])
}

func subscriptionHeaders(token, userAgent string) map[string]string {
	headers := map[string]string{
		"User-Agent": strings.TrimSpace(userAgent),
	}
	if headers["User-Agent"] == "" {
		headers["User-Agent"] = "Haruki-Toolbox-Backend"
	}
	if auth := bearerAuth(token); auth != "" {
		headers["Authorization"] = auth
	}
	return headers
}

func subscriptionJSONHeaders(token, userAgent string) map[string]string {
	headers := subscriptionHeaders(token, userAgent)
	headers["Content-Type"] = "application/json"
	return headers
}

func bearerAuth(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		return token
	}
	return "Bearer " + token
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
			keyText, ok := key.(string)
			if ok {
				result[keyText] = value
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

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return fmt.Sprint(value)
	}
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		if n, err := typed.Int64(); err == nil {
			return int(n)
		}
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(typed))
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
	case int32:
		return float64(typed)
	case int64:
		return float64(typed)
	case float64:
		return typed
	case json.Number:
		n, _ := typed.Float64()
		return n
	case string:
		n, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return n
	}
	return 0
}

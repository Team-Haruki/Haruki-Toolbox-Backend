package handler

import (
	"context"
	"fmt"
	"haruki-suite/utils"
	harukiVersion "haruki-suite/version"
	"strings"
	"sync"
	"time"
)

const webhookCallbackTimeout = 10 * time.Second

func (h *DataHandler) CallbackWebhookAPI(ctx context.Context, url, bearer string) {
	h.Logger.Infof("Calling back WebHook API: %s", url)
	headers := map[string]string{
		"User-Agent": fmt.Sprintf("Haruki-Toolbox-Backend/%s", harukiVersion.Version),
	}
	if bearer != "" {
		headers["Authorization"] = "Bearer " + bearer
	}
	statusCode, _, _, err := h.HttpClient.Request(ctx, "POST", url, headers, nil)
	if err != nil {
		h.Logger.Errorf("WebHook API call failed: %v", err)
		return
	}
	if statusCode == 200 {
		h.Logger.Infof("Called back WebHook API %s successfully.", url)
	} else {
		h.Logger.Errorf("Called back WebHook API %s failed, status code: %d", url, statusCode)
	}
}

func parseWebhookCallback(cb map[string]any) (string, string, bool) {
	rawURL, ok := cb["callback_url"]
	if !ok {
		return "", "", false
	}
	url, ok := rawURL.(string)
	if !ok || strings.TrimSpace(url) == "" {
		return "", "", false
	}
	bearer, _ := cb["bearer"].(string)
	if bearer == "" {
		// Backward-compat for old data that may contain uppercase field names.
		if legacy, ok := cb["Bearer"].(string); ok {
			bearer = legacy
		}
	}
	return url, bearer, true
}

func (h *DataHandler) CallWebhook(
	ctx context.Context,
	userID int64,
	server utils.SupportedDataUploadServer,
	dataType utils.UploadDataType,
) {
	callbacks, err := h.DBManager.Mongo.GetWebhookPushAPI(ctx, userID, string(server), string(dataType))
	if err != nil || len(callbacks) == 0 {
		return
	}
	var wg sync.WaitGroup
	for _, cb := range callbacks {
		url, bearer, ok := parseWebhookCallback(cb)
		if !ok {
			h.Logger.Warnf("Skip invalid webhook callback payload: %v", cb)
			continue
		}
		url = strings.ReplaceAll(url, "{user_id}", fmt.Sprint(userID))
		url = strings.ReplaceAll(url, "{server}", string(server))
		url = strings.ReplaceAll(url, "{data_type}", string(dataType))
		wg.Add(1)
		go func(u, b string) {
			defer wg.Done()
			h.CallbackWebhookAPI(ctx, u, b)
		}(url, bearer)
	}
	wg.Wait()
}

func (h *DataHandler) CallWebhookAsync(userID int64, server utils.SupportedDataUploadServer, dataType utils.UploadDataType) {
	ctx, cancel := context.WithTimeout(context.Background(), webhookCallbackTimeout)
	defer cancel()
	h.CallWebhook(ctx, userID, server, dataType)
}

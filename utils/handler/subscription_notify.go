package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
)

func (h *DataHandler) notifyHMESBirthdayEvent(ctx context.Context, event *BirthdayMonitorEvent) error {
	cfg := config.Cfg.Subscription
	if event == nil || strings.TrimSpace(cfg.HMESInternalBaseURL) == "" {
		if event != nil {
			h.Logger.Warnf("birthday subscription HMES notify skipped: hmes_internal_base_url is not configured event=%s subscription=%s", event.EventID, event.SubscriptionID)
		}
		return nil
	}
	body, err := json.Marshal(hmesEventNotifyRequest{
		EventID:             event.EventID,
		SubscriptionID:      event.SubscriptionID,
		SubscriptionVersion: event.SubscriptionVersion,
		PayloadRef:          event.PayloadRef,
		EmptyResult:         event.EmptyResult,
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
	h.Logger.Infof("birthday subscription HMES notify sent: event=%s subscription=%s version=%s empty_result=%t", event.EventID, event.SubscriptionID, event.SubscriptionVersion, event.EmptyResult)
	return nil
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

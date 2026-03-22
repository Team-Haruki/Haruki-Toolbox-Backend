package handler

import (
	"context"
	"fmt"
	"haruki-suite/utils"
	dbManager "haruki-suite/utils/database/postgresql"
	harukiVersion "haruki-suite/version"
	"net"
	urlpkg "net/url"
	"strings"
	"sync"
	"time"
)

const webhookCallbackTimeout = 10 * time.Second

var webhookIPAddrLookup = net.DefaultResolver.LookupIPAddr

func isHTTPSuccessStatus(statusCode int) bool {
	return statusCode >= 200 && statusCode < 300
}

func isWebhookCallbackHostAllowed(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return false
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !isPrivateOrLocalIP(ip)
	}
	return true
}

func isPrivateOrLocalIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
}

func ValidateWebhookCallbackURL(rawURL string) (string, bool) {
	trimmedURL := strings.TrimSpace(rawURL)
	parsed, err := urlpkg.Parse(trimmedURL)
	if err != nil {
		return "", false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", false
	}
	if parsed.User != nil {
		return "", false
	}
	hostname := strings.TrimSpace(parsed.Hostname())
	if !isWebhookCallbackHostAllowed(hostname) {
		return "", false
	}
	if ip := net.ParseIP(hostname); ip == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		addrs, err := webhookIPAddrLookup(ctx, hostname)
		if err != nil || len(addrs) == 0 {
			return "", false
		}
		for _, addr := range addrs {
			if isPrivateOrLocalIP(addr.IP) {
				return "", false
			}
		}
	}
	return trimmedURL, true
}

func (h *DataHandler) CallbackWebhookAPI(ctx context.Context, url, bearer string) {
	h.Logger.Infof("Calling back WebHook API: %s", url)
	headers := map[string]string{
		"User-Agent": fmt.Sprintf("Haruki-Toolbox-Backend/%s", harukiVersion.Version),
	}
	if bearer != "" {
		headers["Authorization"] = "Bearer " + bearer
	}
	if validatedURL, ok := ValidateWebhookCallbackURL(url); ok {
		url = validatedURL
	} else {
		h.Logger.Warnf("Skipped webhook callback after URL validation failed: %s", url)
		return
	}
	statusCode, _, _, err := h.HttpClient.RequestNoRedirect(ctx, "POST", url, headers, nil)
	if err != nil {
		h.Logger.Errorf("WebHook API call failed: %v", err)
		return
	}
	if isHTTPSuccessStatus(statusCode) {
		h.Logger.Infof("Called back WebHook API %s successfully.", url)
	} else {
		h.Logger.Errorf("Called back WebHook API %s failed, status code: %d", url, statusCode)
	}
}

func parseWebhookCallback(cb any) (string, string, bool) {
	switch typed := cb.(type) {
	case map[string]any:
		rawURL, ok := typed["callback_url"]
		if !ok {
			return "", "", false
		}
		url, ok := rawURL.(string)
		if !ok || strings.TrimSpace(url) == "" {
			return "", "", false
		}
		validatedURL, ok := ValidateWebhookCallbackURL(url)
		if !ok {
			return "", "", false
		}
		bearer, _ := typed["bearer"].(string)
		if bearer == "" {
			if legacy, ok := typed["Bearer"].(string); ok {
				bearer = legacy
			}
		}
		return validatedURL, bearer, true
	case dbManager.WebhookCallback:
		if strings.TrimSpace(typed.CallbackURL) == "" {
			return "", "", false
		}
		validatedURL, ok := ValidateWebhookCallbackURL(typed.CallbackURL)
		if !ok {
			return "", "", false
		}
		return validatedURL, strings.TrimSpace(typed.Bearer), true
	default:
		return "", "", false
	}
}

func (h *DataHandler) CallWebhook(
	ctx context.Context,
	userID int64,
	server utils.SupportedDataUploadServer,
	dataType utils.UploadDataType,
) {
	if h == nil || !h.WebhookEnabled || h.DBManager == nil || h.DBManager.DB == nil {
		return
	}
	callbacks, err := h.DBManager.DB.GetWebhookPushAPI(ctx, userID, string(server), string(dataType))
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

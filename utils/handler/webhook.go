package handler

import (
	"context"
	"fmt"
	"haruki-suite/utils"
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

func validateWebhookCallbackURL(rawURL string) (string, bool) {
	parsed, err := urlpkg.Parse(strings.TrimSpace(rawURL))
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
	return parsed.String(), true
}

func (h *DataHandler) CallbackWebhookAPI(ctx context.Context, url, bearer string) {
	h.Logger.Infof("Calling back WebHook API: %s", url)
	headers := map[string]string{
		"User-Agent": fmt.Sprintf("Haruki-Toolbox-Backend/%s", harukiVersion.Version),
	}
	if bearer != "" {
		headers["Authorization"] = "Bearer " + bearer
	}
	if validatedURL, ok := validateWebhookCallbackURL(url); ok {
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

func parseWebhookCallback(cb map[string]any) (string, string, bool) {
	rawURL, ok := cb["callback_url"]
	if !ok {
		return "", "", false
	}
	url, ok := rawURL.(string)
	if !ok || strings.TrimSpace(url) == "" {
		return "", "", false
	}
	validatedURL, ok := validateWebhookCallbackURL(url)
	if !ok {
		return "", "", false
	}
	bearer, _ := cb["bearer"].(string)
	if bearer == "" {
		if legacy, ok := cb["Bearer"].(string); ok {
			bearer = legacy
		}
	}
	return validatedURL, bearer, true
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

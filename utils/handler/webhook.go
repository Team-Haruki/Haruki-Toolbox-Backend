package handler

import (
	"context"
	"fmt"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	dbManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	harukiVersion "github.com/Team-Haruki/Haruki-Toolbox-Backend/version"
	"io"
	"net"
	stdhttp "net/http"
	"net/netip"
	urlpkg "net/url"
	"strings"
	"sync"
	"time"
)

const webhookCallbackTimeout = 10 * time.Second

var webhookIPAddrLookup = net.DefaultResolver.LookupIPAddr

var webhookBlockedPublicPrefixes = []netip.Prefix{
	// RFC 6598 shared address space is also used by Tailscale. Go's
	// net.IP.IsPrivate intentionally does not classify it as private, but a
	// webhook must never use it to reach services on the deployment overlay.
	netip.MustParsePrefix("100.64.0.0/10"),
}

// webhookSafeDialContext resolves the target host at connection time and dials
// only public IPs, defeating DNS-rebinding: validating the URL's DNS up front is
// not enough because the request re-resolves at dial time. The connection is
// pinned to an IP that was just re-checked here.
func webhookSafeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateOrLocalIP(ip) {
			return nil, fmt.Errorf("blocked private webhook address %s", ip)
		}
		var dialer net.Dialer
		return dialer.DialContext(ctx, network, addr)
	}
	addrs, err := webhookIPAddrLookup(ctx, host)
	if err != nil || len(addrs) == 0 {
		return nil, fmt.Errorf("resolve webhook host %q: %w", host, err)
	}
	for _, a := range addrs {
		if isPrivateOrLocalIP(a.IP) {
			return nil, fmt.Errorf("blocked private webhook address %s", a.IP)
		}
	}
	var dialer net.Dialer
	var lastErr error
	for _, a := range addrs {
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(a.IP.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no dialable address for webhook host %q", host)
	}
	return nil, lastErr
}

// webhookCallbackHTTPClient is dedicated to user-supplied webhook callback URLs:
// SSRF-guarded dialer, no redirects, no internal proxy.
var webhookCallbackHTTPClient = &stdhttp.Client{
	Timeout: webhookCallbackTimeout,
	CheckRedirect: func(*stdhttp.Request, []*stdhttp.Request) error {
		return stdhttp.ErrUseLastResponse
	},
	Transport: &stdhttp.Transport{
		Proxy:                 nil,
		DialContext:           webhookSafeDialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: webhookCallbackTimeout,
		MaxIdleConns:          16,
	},
}

func doWebhookCallback(ctx context.Context, url string, headers map[string]string) (int, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodPost, url, nil)
	if err != nil {
		return 0, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := webhookCallbackHTTPClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func(body io.ReadCloser) { _ = body.Close() }(resp.Body)
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	return resp.StatusCode, nil
}

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
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	address = address.Unmap()
	for _, prefix := range webhookBlockedPublicPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
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
	statusCode, err := doWebhookCallback(ctx, url, headers)
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
	case dbManager.OAuth2ClientWebhookCallback:
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

func applyWebhookPlaceholders(rawURL string, userID int64, server utils.SupportedDataUploadServer, dataType utils.UploadDataType) string {
	url := strings.ReplaceAll(rawURL, "{user_id}", fmt.Sprint(userID))
	url = strings.ReplaceAll(url, "{server}", string(server))
	url = strings.ReplaceAll(url, "{data_type}", string(dataType))
	return url
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
		url = applyWebhookPlaceholders(url, userID, server, dataType)
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

func (h *DataHandler) CallOAuth2Webhook(
	ctx context.Context,
	userID int64,
	server utils.SupportedDataUploadServer,
	dataType utils.UploadDataType,
) {
	if h == nil || !h.WebhookEnabled || h.DBManager == nil || h.DBManager.DB == nil || h.OAuth2WebhookAuthorizer == nil {
		return
	}
	if !h.OAuth2WebhookAuthorizer.Enabled() {
		return
	}

	owner, err := h.DBManager.DB.GetOAuth2WebhookOwnerForGameAccount(ctx, userID, string(server))
	if err != nil {
		if !dbManager.IsNotFound(err) {
			h.Logger.Warnf("Failed to query OAuth2 webhook owner: server=%s userID=%d err=%v", server, userID, err)
		}
		return
	}
	if owner == nil || owner.UserID == "" || owner.Banned {
		return
	}

	clientIDs, err := h.OAuth2WebhookAuthorizer.AuthorizedClientIDs(ctx, owner.UserID, owner.KratosIdentityID)
	if err != nil {
		h.Logger.Warnf("Failed to query OAuth2 consent sessions for webhook: owner=%s err=%v", owner.UserID, err)
		return
	}
	if len(clientIDs) == 0 {
		return
	}

	callbacks, err := h.DBManager.DB.GetOAuth2ClientWebhookCallbacks(ctx, clientIDs)
	if err != nil {
		h.Logger.Warnf("Failed to query OAuth2 webhook callbacks: owner=%s err=%v", owner.UserID, err)
		return
	}
	if len(callbacks) == 0 {
		return
	}

	var wg sync.WaitGroup
	for _, cb := range callbacks {
		url, bearer, ok := parseWebhookCallback(cb)
		if !ok {
			h.Logger.Warnf("Skip invalid OAuth2 webhook callback payload: %v", cb)
			continue
		}
		url = applyWebhookPlaceholders(url, userID, server, dataType)
		wg.Add(1)
		go func(u, b string) {
			defer wg.Done()
			h.CallbackWebhookAPI(ctx, u, b)
		}(url, bearer)
	}
	wg.Wait()
}

func (h *DataHandler) CallOAuth2WebhookAsync(userID int64, server utils.SupportedDataUploadServer, dataType utils.UploadDataType) {
	ctx, cancel := context.WithTimeout(context.Background(), webhookCallbackTimeout)
	defer cancel()
	h.CallOAuth2Webhook(ctx, userID, server, dataType)
}

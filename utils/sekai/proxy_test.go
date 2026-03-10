package sekai

import (
	"errors"
	harukiConfig "haruki-suite/config"
	harukiUtils "haruki-suite/utils"
	"strings"
	"testing"
)

func TestFilterHeaders(t *testing.T) {
	t.Parallel()

	in := map[string]string{
		"User-Agent":      "ua",
		"X-App-Version":   "1",
		"X-Not-Allowed":   "x",
		"Content-Type":    "application/octet-stream",
		"X-Session-Token": "st",
	}
	filtered := filterHeaders(in)

	if filtered["user-agent"] != "ua" {
		t.Fatalf("user-agent should be kept")
	}
	if filtered["x-app-version"] != "1" {
		t.Fatalf("x-app-version should be kept")
	}
	if _, ok := filtered["x-not-allowed"]; ok {
		t.Fatalf("x-not-allowed should be removed")
	}
	if filtered["content-type"] != "application/octet-stream" {
		t.Fatalf("content-type should be kept")
	}
}

func TestBuildAPIEndpointsReflectsConfig(t *testing.T) {
	originalCfg := harukiConfig.Cfg
	t.Cleanup(func() {
		harukiConfig.Cfg = originalCfg
	})

	harukiConfig.Cfg.SekaiClient.JPServerAPIHost = "jp.example.com"
	harukiConfig.Cfg.SekaiClient.ENServerAPIHost = "en.example.com"
	harukiConfig.Cfg.SekaiClient.TWServerAPIHost = "tw.example.com"
	harukiConfig.Cfg.SekaiClient.KRServerAPIHost = "kr.example.com"
	harukiConfig.Cfg.SekaiClient.CNServerAPIHost = "cn.example.com"

	endpoints := GetAPIEndpoint()
	if got := endpoints[harukiUtils.SupportedDataUploadServerJP][0]; got != "https://jp.example.com/api" {
		t.Fatalf("JP endpoint = %q", got)
	}
	if got := endpoints[harukiUtils.SupportedDataUploadServerEN][1]; got != "en.example.com" {
		t.Fatalf("EN host = %q", got)
	}
}

func TestResolveProxyEndpoint_InvalidServer(t *testing.T) {
	t.Parallel()

	_, _, err := resolveProxyEndpoint(harukiUtils.SupportedDataUploadServer("xx"))
	if err == nil {
		t.Fatalf("resolveProxyEndpoint should fail for unsupported server")
	}
	if !errors.Is(err, ErrInvalidServer) {
		t.Fatalf("resolveProxyEndpoint err=%v, want ErrInvalidServer", err)
	}
}

func TestBuildProxyPath(t *testing.T) {
	t.Parallel()

	path, err := buildProxyPath(harukiUtils.UploadDataTypeSuite, httpMethodGet, 123, nil)
	if err != nil {
		t.Fatalf("buildProxyPath suite err=%v", err)
	}
	if path != "/suite/user/123" {
		t.Fatalf("buildProxyPath suite=%q", path)
	}

	id := int64(99)
	path, err = buildProxyPath(harukiUtils.UploadDataTypeMysekaiBirthdayParty, httpMethodPost, 123, &id)
	if err != nil {
		t.Fatalf("buildProxyPath birthday err=%v", err)
	}
	if path != "/user/123/mysekai/birthday-party/99/delivery" {
		t.Fatalf("buildProxyPath birthday=%q", path)
	}

	_, err = buildProxyPath(harukiUtils.UploadDataTypeMysekaiBirthdayParty, httpMethodPost, 123, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("buildProxyPath missing birthday id should return APIError, got %T", err)
	}

	_, err = buildProxyPath(harukiUtils.UploadDataType("unknown"), httpMethodGet, 123, nil)
	if err == nil {
		t.Fatalf("buildProxyPath should fail for invalid data type")
	}
	if !errors.Is(err, ErrInvalidDataType) {
		t.Fatalf("buildProxyPath err=%v, want ErrInvalidDataType", err)
	}
}

func TestAppendQueryParams(t *testing.T) {
	t.Parallel()

	url := appendQueryParams("https://example.com/a", map[string]string{
		"z": "2",
		"a": "1",
	})
	if !strings.HasPrefix(url, "https://example.com/a?") {
		t.Fatalf("unexpected url prefix: %q", url)
	}
	if !strings.Contains(url, "a=1") || !strings.Contains(url, "z=2") {
		t.Fatalf("query params not encoded correctly: %q", url)
	}
}

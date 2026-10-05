package upload

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	platform "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/upload"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

func TestProxyMetadataParsing(t *testing.T) {
	for _, v := range []string{"3.0.0", "3.0.0-preview", "3.0.0-preview.10+gabcdef", "3.0.0-beta.2", "3.0.0-dev.12", "3.0.0-rc.1"} {
		for _, p := range []string{"Windows", "macOS", "Android", "iOS", "Linux", "Unknown"} {
			m, err := parseProxyUserAgent("HarukiProxy/v" + v + " (app_arch=x64; platform=" + p + "; os_arch=arm64; future=hello)")
			if err != nil || m.Version != v || m.OSArch != "arm64" || m.AppArch != "x64" || m.Format != "structured" {
				t.Fatalf("%s %s: %+v %v", p, v, m, err)
			}
		}
	}
	for _, ua := range []string{
		"HarukiProxy/v3.0.0", "HarukiProxy/v3.0.0 (os_arch=arm64)", "HarukiProxy/v3.0.0 (platform=Android; platform=iOS)",
		"HarukiProxy/v3.0.0 (platform=Android;)", "HarukiProxy/v3.0.0 (platform=Android) trailing",
		"HarukiProxy/v03.0.0 (platform=Android)", "HarukiProxy/v3.0.0-preview.01 (platform=Android)",
		"HarukiProxy/v3.0.0 (platform=(Android))", "HarukiProxy/v3.0.0 (platform=Android\n)",
		"HarukiProxy/v3.0.0 (platform=Android; os_version=" + strings.Repeat("a", 65) + ")", strings.Repeat("a", 513),
	} {
		if _, err := parseProxyUserAgent(ua); err == nil {
			t.Fatalf("accepted %q", ua)
		}
	}
}

func TestProxyV3ResponseContract(t *testing.T) {
	policy, err := platform.CompileClientPolicy([]string{"stable", "preview"}, map[string]string{"stable": "3.0.0", "preview": "3.0.0-preview.2"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ua, secret, code string
		status           int
	}{
		{"garbage", "wrong", "invalid_client_metadata", 400},
		{"HarukiProxy/v3.0.0", "secret", "invalid_client_metadata", 400},
		{"HarukiProxy/v3.0.0-preview.1 (platform=Android)", "secret", "client_version_unsupported", 400},
		{"HarukiProxy/v3.0.0-dev.1 (platform=Android)", "secret", "client_channel_disabled", 400},
		{"HarukiProxy/v3.0.0-preview.2+build.1 (platform=Android)", "secret", "", 200},
	} {
		app := fiber.New()
		app.Post("/", validateProxyV3Client(nil, Dependencies{HarukiProxyV3ClientPolicy: policy}), func(c fiber.Ctx) error { return proxyResponse(c, 200, "", "success", false, nil) })
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("User-Agent", tc.ua)
		req.Header.Set("X-Haruki-Toolbox-Secret", tc.secret)
		req.Header.Set("X-Request-ID", "untrusted")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			UpdatedData proxyResponseData `json:"updatedData"`
			Data        any               `json:"data"`
		}
		err = json.NewDecoder(resp.Body).Decode(&body)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != tc.status || body.UpdatedData.ErrorCode != tc.code {
			t.Fatalf("%s: %d %+v %v", tc.ua, resp.StatusCode, body, err)
		}
		if _, err := uuid.Parse(body.UpdatedData.RequestID); err != nil || body.UpdatedData.RequestID != resp.Header.Get("X-Request-ID") {
			t.Fatal("invalid request ID")
		}
		if body.Data != nil || body.UpdatedData.Retryable {
			t.Fatal("unexpected response contract")
		}
		if tc.status == 401 && body.UpdatedData.ClientPolicy != nil {
			t.Fatal("auth leaks policy")
		}
	}
}

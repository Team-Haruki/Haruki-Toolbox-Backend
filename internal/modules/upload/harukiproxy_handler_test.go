package upload

import (
	"bytes"
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	platform "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/upload"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/background"
)

type proxyTestResponse struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
	Data    struct {
		ErrorCode string `json:"error_code"`
		RequestID string `json:"request_id"`
		Retryable bool   `json:"retryable"`
	} `json:"updatedData"`
}

// newLegacyProxyApp mounts the legacy upload handler and exposes the request's
// attempt record so tests can assert what the audit log would receive.
func newLegacyProxyApp(t *testing.T, helper *harukiAPIHelper.HarukiToolboxRouterHelpers, v3 bool, attempt **platform.Attempt) *fiber.App {
	t.Helper()
	deps := testUploadDependencies()
	deps.BackgroundTasks = background.InlineRunner{}
	app := fiber.New()
	app.Post("/:server/:user_id/:data_type/upload",
		func(c fiber.Ctx) error {
			*attempt = proxyAttempt(c)
			return c.Next()
		},
		handleHarukiProxyUploadVersion(helper, deps, v3),
	)
	return app
}

func doProxyRequest(t *testing.T, app *fiber.App, path, userAgent string, body []byte) (int, proxyTestResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var decoded proxyTestResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if resp.Header.Get("X-Request-ID") == "" || decoded.Data.RequestID != resp.Header.Get("X-Request-ID") {
		t.Fatalf("request id header %q does not match body %q", resp.Header.Get("X-Request-ID"), decoded.Data.RequestID)
	}
	return resp.StatusCode, decoded
}

func TestHarukiProxyUploadRejectsInvalidPathParameters(t *testing.T) {
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{HarukiProxyUnpackKey: "unpack"}
	var attempt *platform.Attempt
	app := newLegacyProxyApp(t, helper, false, &attempt)
	for path, message := range map[string]string{
		"/xx/123/suite/upload":   "invalid server",
		"/jp/123/unknown/upload": "invalid data_type",
		"/jp/abc/suite/upload":   "invalid user_id",
		"/jp/0/suite/upload":     "invalid user_id",
		"/jp/-5/mysekai/upload":  "invalid user_id",
		"/jp/1e3/suite/upload":   "invalid user_id",
	} {
		t.Run(path, func(t *testing.T) {
			status, body := doProxyRequest(t, app, path, "HarukiProxy/v1.2.3", nil)
			if status != fiber.StatusBadRequest || body.Message != message || body.Data.ErrorCode != "invalid_upload_payload" || body.Data.Retryable {
				t.Fatalf("got %d %+v, want 400 %q", status, body, message)
			}
		})
	}
}

func TestHarukiProxyUploadDecryptFailureRecordsAttempt(t *testing.T) {
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{HarukiProxyUnpackKey: "unpack"}
	var attempt *platform.Attempt
	app := newLegacyProxyApp(t, helper, false, &attempt)

	// Sealed for a different path: the AAD binds server|user_id|data_type.
	sealed, err := packForTest([]byte(`{}`), "jp|999|suite", "unpack")
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{"truncated": []byte("short"), "wrong aad": sealed} {
		t.Run(name, func(t *testing.T) {
			status, resp := doProxyRequest(t, app, "/jp/123/suite/upload", "HarukiProxy/v2.0.1-beta", body)
			if status != fiber.StatusBadRequest || resp.Message != "failed to decrypt request body" || resp.Data.ErrorCode != "payload_decryption_failed" {
				t.Fatalf("got %d %+v", status, resp)
			}
			if attempt.FailureStage != "decrypt" || attempt.ErrorCode != "payload_decryption_failed" || attempt.HTTPStatus != 400 {
				t.Fatalf("attempt failure not recorded: %+v", attempt)
			}
			if attempt.Client.Protocol != "2" || attempt.Client.Name != "HarukiProxy" || attempt.Client.Format != "legacy" || attempt.Client.Version != "2.0.1-beta" {
				t.Fatalf("legacy client metadata = %+v", attempt.Client)
			}
		})
	}

	// A User-Agent that does not match the legacy pattern leaves the version empty.
	if status, _ := doProxyRequest(t, app, "/jp/123/suite/upload", "curl/8", []byte("short")); status != fiber.StatusBadRequest {
		t.Fatalf("status = %d", status)
	}
	if attempt.Client.Version != "" || attempt.Client.Name != "HarukiProxy" {
		t.Fatalf("unparsable User-Agent produced client metadata %+v", attempt.Client)
	}
}

func TestHarukiProxyUploadMissingUnpackKeyFailsDecrypt(t *testing.T) {
	var attempt *platform.Attempt
	app := newLegacyProxyApp(t, &harukiAPIHelper.HarukiToolboxRouterHelpers{}, false, &attempt)
	sealed, err := packForTest([]byte(`{}`), "jp|123|suite", "unpack")
	if err != nil {
		t.Fatal(err)
	}
	status, resp := doProxyRequest(t, app, "/jp/123/suite/upload", "HarukiProxy/v1.0.0", sealed)
	if status != fiber.StatusBadRequest || resp.Data.ErrorCode != "payload_decryption_failed" {
		t.Fatalf("got %d %+v", status, resp)
	}
}

func TestHarukiProxyV3UploadRequiresOAuth2Subject(t *testing.T) {
	var attempt *platform.Attempt
	app := newLegacyProxyApp(t, &harukiAPIHelper.HarukiToolboxRouterHelpers{}, true, &attempt)
	status, resp := doProxyRequest(t, app, "/jp/123/suite/upload", "HarukiProxy/3.0.0", []byte(`{}`))
	if status != fiber.StatusUnauthorized || resp.Data.ErrorCode != "invalid_token" || resp.Message != "OAuth2 authentication required" {
		t.Fatalf("got %d %+v", status, resp)
	}
	// The v3 handler must not stamp legacy client metadata.
	if attempt.Client.Name != "" || attempt.Client.Format != "" {
		t.Fatalf("v3 attempt carries legacy metadata: %+v", attempt.Client)
	}
}

func TestValidateHarukiProxyClientHeaderRejectsMetadata(t *testing.T) {
	for name, tc := range map[string]struct {
		minVersion, userAgent, code, message string
	}{
		"wrong name":          {"v1.0.0", "OtherProxy/v1.2.3", "invalid_client_metadata", "Invalid User-Agent name"},
		"bad minimum version": {"latest", "HarukiProxy/v1.2.3", "invalid_client_metadata", "Invalid version string"},
	} {
		t.Run(name, func(t *testing.T) {
			helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
				HarukiProxyUserAgent: "HarukiProxy",
				HarukiProxyVersion:   tc.minVersion,
				HarukiProxySecret:    "secret",
			}
			app := fiber.New()
			app.Post("/", validateHarukiProxyClientHeader(helper), func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) })
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.Header.Set("User-Agent", tc.userAgent)
			req.Header.Set("X-Haruki-Toolbox-Secret", "secret")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var body proxyTestResponse
			if err := json.UnmarshalRead(resp.Body, &body); err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != fiber.StatusBadRequest || body.Data.ErrorCode != tc.code || body.Message != tc.message {
				t.Fatalf("got %d %+v", resp.StatusCode, body)
			}
		})
	}
}

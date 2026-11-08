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

// newProxyApp mounts the upload handler and exposes the request's attempt
// record so tests can assert what the audit log would receive.
func newProxyApp(t *testing.T, helper *harukiAPIHelper.HarukiToolboxRouterHelpers, attempt **platform.Attempt) *fiber.App {
	t.Helper()
	deps := testUploadDependencies()
	deps.BackgroundTasks = background.InlineRunner{}
	app := fiber.New()
	app.Post("/:server/:user_id/:data_type/upload",
		func(c fiber.Ctx) error {
			*attempt = proxyAttempt(c)
			return c.Next()
		},
		handleHarukiProxyUpload(helper, deps),
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
	var attempt *platform.Attempt
	app := newProxyApp(t, &harukiAPIHelper.HarukiToolboxRouterHelpers{}, &attempt)
	for path, message := range map[string]string{
		"/xx/123/suite/upload":   "invalid server",
		"/jp/123/unknown/upload": "invalid data_type",
		"/jp/abc/suite/upload":   "invalid user_id",
		"/jp/0/suite/upload":     "invalid user_id",
		"/jp/-5/mysekai/upload":  "invalid user_id",
		"/jp/1e3/suite/upload":   "invalid user_id",
	} {
		t.Run(path, func(t *testing.T) {
			status, body := doProxyRequest(t, app, path, "HarukiProxy/v3.0.0 (platform=Android)", nil)
			if status != fiber.StatusBadRequest || body.Message != message || body.Data.ErrorCode != "invalid_upload_payload" || body.Data.Retryable {
				t.Fatalf("got %d %+v, want 400 %q", status, body, message)
			}
		})
	}
}

func TestHarukiProxyV3UploadRequiresOAuth2Subject(t *testing.T) {
	var attempt *platform.Attempt
	app := newProxyApp(t, &harukiAPIHelper.HarukiToolboxRouterHelpers{}, &attempt)
	status, resp := doProxyRequest(t, app, "/jp/123/suite/upload", "HarukiProxy/3.0.0", []byte(`{}`))
	if status != fiber.StatusUnauthorized || resp.Data.ErrorCode != "invalid_token" || resp.Message != "OAuth2 authentication required" {
		t.Fatalf("got %d %+v", status, resp)
	}
	// The handler stamps no client metadata of its own (validateProxyV3Client does).
	if attempt.Client.Name != "" || attempt.Client.Format != "" {
		t.Fatalf("attempt carries client metadata: %+v", attempt.Client)
	}
}

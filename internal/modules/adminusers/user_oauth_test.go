package adminusers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestParseAdminOAuthIncludeRevoked(t *testing.T) {
	t.Run("default false", func(t *testing.T) {
		includeRevoked, err := parseAdminOAuthIncludeRevoked("")
		if err != nil {
			t.Fatalf("parseAdminOAuthIncludeRevoked returned error: %v", err)
		}
		if includeRevoked {
			t.Fatalf("includeRevoked = true, want false")
		}
	})

	t.Run("accept true", func(t *testing.T) {
		includeRevoked, err := parseAdminOAuthIncludeRevoked("true")
		if err != nil {
			t.Fatalf("parseAdminOAuthIncludeRevoked returned error: %v", err)
		}
		if !includeRevoked {
			t.Fatalf("includeRevoked = false, want true")
		}
	})

	t.Run("reject invalid bool", func(t *testing.T) {
		if _, err := parseAdminOAuthIncludeRevoked("maybe"); err == nil {
			t.Fatalf("expected invalid include_revoked to fail")
		}
	})
}

func TestParseAdminRevokeOAuthClientID(t *testing.T) {
	app := fiber.New()
	app.Post("/", func(c fiber.Ctx) error {
		clientID, err := parseAdminRevokeOAuthClientID(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.Status(fiberErr.Code).SendString(fiberErr.Message)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendString(clientID)
	})

	testCases := []struct {
		Name        string
		ContentType string
		Body        string
		WantStatus  int
		WantBody    string
	}{
		{
			Name:       "empty body",
			WantStatus: fiber.StatusOK,
			WantBody:   "",
		},
		{
			Name:        "json camel case",
			ContentType: contentTypeApplicationJSON,
			Body:        `{"clientId":"abc"}`,
			WantStatus:  fiber.StatusOK,
			WantBody:    "abc",
		},
		{
			Name:        "json snake case",
			ContentType: contentTypeApplicationJSON,
			Body:        `{"client_id":"xyz"}`,
			WantStatus:  fiber.StatusOK,
			WantBody:    "xyz",
		},
		{
			Name:        "form encoded",
			ContentType: contentTypeApplicationFormURLEncoded,
			Body:        "client_id=form-client",
			WantStatus:  fiber.StatusOK,
			WantBody:    "form-client",
		},
		{
			Name:       "missing content-type json fallback",
			Body:       `{"clientId":"no-header-json"}`,
			WantStatus: fiber.StatusOK,
			WantBody:   "no-header-json",
		},
		{
			Name:       "missing content-type form fallback",
			Body:       "client_id=no-header-form",
			WantStatus: fiber.StatusOK,
			WantBody:   "no-header-form",
		},
		{
			Name:        "invalid json payload",
			ContentType: contentTypeApplicationJSON,
			Body:        `{"clientId":`,
			WantStatus:  fiber.StatusBadRequest,
		},
		{
			Name:        "invalid form payload",
			ContentType: contentTypeApplicationFormURLEncoded,
			Body:        "client_id=%ZZ",
			WantStatus:  fiber.StatusBadRequest,
		},
		{
			Name:        "unsupported content type",
			ContentType: "text/plain",
			Body:        "client_id=abc",
			WantStatus:  fiber.StatusBadRequest,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.Name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(testCase.Body))
			if testCase.ContentType != "" {
				req.Header.Set("Content-Type", testCase.ContentType)
			}

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test returned error: %v", err)
			}

			if resp.StatusCode != testCase.WantStatus {
				t.Fatalf("status code = %d, want %d", resp.StatusCode, testCase.WantStatus)
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("io.ReadAll returned error: %v", err)
			}
			if testCase.WantStatus == fiber.StatusOK && string(body) != testCase.WantBody {
				t.Fatalf("response body = %q, want %q", string(body), testCase.WantBody)
			}
		})
	}
}

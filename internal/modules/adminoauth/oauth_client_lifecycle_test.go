package adminoauth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestParseAdminOAuthClientAuthorizationsFilters(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		_, err := parseAdminOAuthClientAuthorizationsFilters(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.SendStatus(fiberErr.Code)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	t.Run("valid filters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?include_revoked=true&page=2&page_size=100", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
	})

	t.Run("invalid include_revoked", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?include_revoked=maybe", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("invalid page size", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?page_size=999", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}

func TestParseAdminOAuthClientRevokeOptions(t *testing.T) {
	app := fiber.New()
	app.Post("/", func(c fiber.Ctx) error {
		options, err := parseAdminOAuthClientRevokeOptions(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.Status(fiberErr.Code).SendString(fiberErr.Message)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}

		target := options.TargetUserID
		if target == "" {
			target = "-"
		}
		return c.SendString(strings.Join([]string{
			target,
			strconv.FormatBool(options.RevokeAuthorizations),
			strconv.FormatBool(options.RevokeTokens),
		}, ","))
	})

	testCases := []struct {
		Name        string
		ContentType string
		Body        string
		WantStatus  int
		WantBody    string
	}{
		{
			Name:       "empty defaults",
			WantStatus: fiber.StatusOK,
			WantBody:   "-,true,true",
		},
		{
			Name:        "json payload",
			ContentType: contentTypeApplicationJSON,
			Body:        `{"targetUserId":"1001","revokeAuthorizations":false,"revokeTokens":true}`,
			WantStatus:  fiber.StatusOK,
			WantBody:    "1001,false,true",
		},
		{
			Name:        "json snake payload",
			ContentType: contentTypeApplicationJSON,
			Body:        `{"target_user_id":"2002","revoke_tokens":false}`,
			WantStatus:  fiber.StatusOK,
			WantBody:    "2002,true,false",
		},
		{
			Name:        "form payload",
			ContentType: contentTypeApplicationFormURLEncoded,
			Body:        "target_user_id=3003&revoke_authorizations=false",
			WantStatus:  fiber.StatusOK,
			WantBody:    "3003,false,true",
		},
		{
			Name:       "fallback form",
			Body:       "targetUserId=4004&revokeTokens=false",
			WantStatus: fiber.StatusOK,
			WantBody:   "4004,true,false",
		},
		{
			Name:        "invalid bool",
			ContentType: contentTypeApplicationFormURLEncoded,
			Body:        "revoke_tokens=abc",
			WantStatus:  fiber.StatusBadRequest,
		},
		{
			Name:        "unsupported content type",
			ContentType: "text/plain",
			Body:        "revoke_tokens=false",
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

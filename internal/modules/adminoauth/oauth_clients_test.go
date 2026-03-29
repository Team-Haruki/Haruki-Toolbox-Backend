package adminoauth

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	harukiOAuth2 "haruki-suite/utils/oauth2"

	"github.com/gofiber/fiber/v3"
	"golang.org/x/crypto/bcrypt"
)

func TestParseAdminOAuthClientStatsWindowHours(t *testing.T) {
	t.Run("default value", func(t *testing.T) {
		hours, err := parseAdminOAuthClientStatsWindowHours("")
		if err != nil {
			t.Fatalf("parseAdminOAuthClientStatsWindowHours returned error: %v", err)
		}
		if hours != defaultAdminOAuthClientStatsWindowHours {
			t.Fatalf("hours = %d, want %d", hours, defaultAdminOAuthClientStatsWindowHours)
		}
	})

	t.Run("valid value", func(t *testing.T) {
		hours, err := parseAdminOAuthClientStatsWindowHours("48")
		if err != nil {
			t.Fatalf("parseAdminOAuthClientStatsWindowHours returned error: %v", err)
		}
		if hours != 48 {
			t.Fatalf("hours = %d, want 48", hours)
		}
	})

	t.Run("invalid integer", func(t *testing.T) {
		if _, err := parseAdminOAuthClientStatsWindowHours("abc"); err == nil {
			t.Fatalf("expected non-integer hours to fail")
		}
	})

	t.Run("exceeds max", func(t *testing.T) {
		if _, err := parseAdminOAuthClientStatsWindowHours("999"); err == nil {
			t.Fatalf("expected oversized hours to fail")
		}
	})
}

func TestNormalizeAdminOAuthClientType(t *testing.T) {
	clientType, err := normalizeAdminOAuthClientType(" confidential ")
	if err != nil {
		t.Fatalf("normalizeAdminOAuthClientType returned error: %v", err)
	}
	if clientType != "confidential" {
		t.Fatalf("clientType = %q, want %q", clientType, "confidential")
	}

	if _, err := normalizeAdminOAuthClientType("desktop"); err == nil {
		t.Fatalf("expected invalid client type to fail")
	}
}

func TestSanitizeAdminOAuthClientID(t *testing.T) {
	clientID, err := sanitizeAdminOAuthClientID("  abc_client-1 ")
	if err != nil {
		t.Fatalf("sanitizeAdminOAuthClientID returned error: %v", err)
	}
	if clientID != "abc_client-1" {
		t.Fatalf("clientID = %q, want %q", clientID, "abc_client-1")
	}

	if _, err := sanitizeAdminOAuthClientID("a"); err == nil {
		t.Fatalf("expected too short clientID to fail")
	}
	if _, err := sanitizeAdminOAuthClientID("client id"); err == nil {
		t.Fatalf("expected invalid character clientID to fail")
	}
}

func TestSanitizeAdminOAuthClientName(t *testing.T) {
	name, err := sanitizeAdminOAuthClientName("  My App ")
	if err != nil {
		t.Fatalf("sanitizeAdminOAuthClientName returned error: %v", err)
	}
	if name != "My App" {
		t.Fatalf("name = %q, want %q", name, "My App")
	}

	if _, err := sanitizeAdminOAuthClientName(" "); err == nil {
		t.Fatalf("expected empty name to fail")
	}
}

func TestSanitizeAdminOAuthClientRedirectURIs(t *testing.T) {
	redirectURIs, err := sanitizeAdminOAuthClientRedirectURIs([]string{
		"https://example.com/callback",
		"https://example.com/callback",
		"myapp://oauth/callback",
	})
	if err != nil {
		t.Fatalf("sanitizeAdminOAuthClientRedirectURIs returned error: %v", err)
	}
	if len(redirectURIs) != 2 {
		t.Fatalf("len(redirectURIs) = %d, want 2", len(redirectURIs))
	}

	if _, err := sanitizeAdminOAuthClientRedirectURIs([]string{"not-a-uri"}); err == nil {
		t.Fatalf("expected invalid redirect uri to fail")
	}
	if _, err := sanitizeAdminOAuthClientRedirectURIs([]string{"https://example.com/callback#frag"}); err == nil {
		t.Fatalf("expected redirect uri with fragment to fail")
	}
}

func TestSanitizeAdminOAuthClientScopes(t *testing.T) {
	scopes, err := sanitizeAdminOAuthClientScopes([]string{
		harukiOAuth2.ScopeOfflineAccess,
		harukiOAuth2.ScopeUserRead,
		harukiOAuth2.ScopeGameDataRead,
		harukiOAuth2.ScopeGameDataRead,
	})
	if err != nil {
		t.Fatalf("sanitizeAdminOAuthClientScopes returned error: %v", err)
	}
	if len(scopes) != 3 {
		t.Fatalf("len(scopes) = %d, want 3", len(scopes))
	}

	if _, err := sanitizeAdminOAuthClientScopes([]string{"admin:all"}); err == nil {
		t.Fatalf("expected invalid scope to fail")
	}
}

func TestGenerateAdminOAuthClientSecret(t *testing.T) {
	plainSecret, hashedSecret, err := generateAdminOAuthClientSecret()
	if err != nil {
		t.Fatalf("generateAdminOAuthClientSecret returned error: %v", err)
	}
	if plainSecret == "" {
		t.Fatalf("plain secret should not be empty")
	}
	if hashedSecret == "" {
		t.Fatalf("hashed secret should not be empty")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hashedSecret), []byte(plainSecret)); err != nil {
		t.Fatalf("generated secret hash mismatch: %v", err)
	}
}

func TestParseAdminOAuthClientIncludeInactive(t *testing.T) {
	v, err := parseAdminOAuthClientIncludeInactive("")
	if err != nil {
		t.Fatalf("parseAdminOAuthClientIncludeInactive returned error: %v", err)
	}
	if !v {
		t.Fatalf("includeInactive = false, want true")
	}

	v, err = parseAdminOAuthClientIncludeInactive("false")
	if err != nil {
		t.Fatalf("parseAdminOAuthClientIncludeInactive returned error: %v", err)
	}
	if v {
		t.Fatalf("includeInactive = true, want false")
	}

	if _, err := parseAdminOAuthClientIncludeInactive("not_bool"); err == nil {
		t.Fatalf("expected invalid include_inactive to fail")
	}
}

func TestParseAdminOAuthClientListPagination(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		page, pageSize, err := parseAdminOAuthClientListPagination(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.Status(fiberErr.Code).SendString(fiberErr.Message)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendString(fmt.Sprintf("%d,%d", page, pageSize))
	})

	t.Run("default pagination", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("io.ReadAll returned error: %v", err)
		}
		if string(body) != "1,100" {
			t.Fatalf("response body = %q, want %q", string(body), "1,100")
		}
	})

	t.Run("custom pagination", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?page=3&page_size=50", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("io.ReadAll returned error: %v", err)
		}
		if string(body) != "3,50" {
			t.Fatalf("response body = %q, want %q", string(body), "3,50")
		}
	})

	t.Run("page size too large", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?page_size=999", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}

func TestParseAdminOAuthClientActiveValue(t *testing.T) {
	app := fiber.New()
	app.Put("/", func(c fiber.Ctx) error {
		active, err := parseAdminOAuthClientActiveValue(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.Status(fiberErr.Code).SendString(fiberErr.Message)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		if active {
			return c.SendString("true")
		}
		return c.SendString("false")
	})

	testCases := []struct {
		Name        string
		ContentType string
		Body        string
		WantStatus  int
		WantBody    string
	}{
		{
			Name:        "json true",
			ContentType: contentTypeApplicationJSON,
			Body:        `{"active":true}`,
			WantStatus:  fiber.StatusOK,
			WantBody:    "true",
		},
		{
			Name:        "json false",
			ContentType: contentTypeApplicationJSON,
			Body:        `{"active":false}`,
			WantStatus:  fiber.StatusOK,
			WantBody:    "false",
		},
		{
			Name:        "form active",
			ContentType: contentTypeApplicationFormURLEncoded,
			Body:        "active=true",
			WantStatus:  fiber.StatusOK,
			WantBody:    "true",
		},
		{
			Name:       "fallback json",
			Body:       `{"active":true}`,
			WantStatus: fiber.StatusOK,
			WantBody:   "true",
		},
		{
			Name:        "missing active",
			ContentType: contentTypeApplicationJSON,
			Body:        `{"enabled":true}`,
			WantStatus:  fiber.StatusBadRequest,
		},
		{
			Name:       "empty body",
			WantStatus: fiber.StatusBadRequest,
		},
		{
			Name:        "invalid bool",
			ContentType: contentTypeApplicationFormURLEncoded,
			Body:        "active=abc",
			WantStatus:  fiber.StatusBadRequest,
		},
		{
			Name:        "unsupported content type",
			ContentType: "text/plain",
			Body:        "active=true",
			WantStatus:  fiber.StatusBadRequest,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.Name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(testCase.Body))
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

func TestParseAdminOAuthClientDeleteOptions(t *testing.T) {
	app := fiber.New()
	app.Delete("/", func(c fiber.Ctx) error {
		options, err := parseAdminOAuthClientDeleteOptions(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.Status(fiberErr.Code).SendString(fiberErr.Message)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		if options.DeleteAuthorizations && options.DeleteTokens {
			return c.SendString("true,true")
		}
		if options.DeleteAuthorizations {
			return c.SendString("true,false")
		}
		if options.DeleteTokens {
			return c.SendString("false,true")
		}
		return c.SendString("false,false")
	})

	testCases := []struct {
		Name        string
		ContentType string
		Body        string
		WantStatus  int
		WantBody    string
	}{
		{
			Name:       "empty body default true true",
			WantStatus: fiber.StatusOK,
			WantBody:   "true,true",
		},
		{
			Name:        "json override both",
			ContentType: contentTypeApplicationJSON,
			Body:        `{"revokeAuthorizations":false,"revokeTokens":false}`,
			WantStatus:  fiber.StatusOK,
			WantBody:    "false,false",
		},
		{
			Name:        "json delete alias override both",
			ContentType: contentTypeApplicationJSON,
			Body:        `{"deleteAuthorizations":false,"deleteTokens":false}`,
			WantStatus:  fiber.StatusOK,
			WantBody:    "false,false",
		},
		{
			Name:        "json snake case",
			ContentType: contentTypeApplicationJSON,
			Body:        `{"revoke_authorizations":false}`,
			WantStatus:  fiber.StatusOK,
			WantBody:    "false,true",
		},
		{
			Name:        "json snake delete alias",
			ContentType: contentTypeApplicationJSON,
			Body:        `{"delete_authorizations":false}`,
			WantStatus:  fiber.StatusOK,
			WantBody:    "false,true",
		},
		{
			Name:        "form override tokens",
			ContentType: contentTypeApplicationFormURLEncoded,
			Body:        "revoke_tokens=false",
			WantStatus:  fiber.StatusOK,
			WantBody:    "true,false",
		},
		{
			Name:        "form delete alias override tokens",
			ContentType: contentTypeApplicationFormURLEncoded,
			Body:        "delete_tokens=false",
			WantStatus:  fiber.StatusOK,
			WantBody:    "true,false",
		},
		{
			Name:       "fallback form without content-type",
			Body:       "revokeAuthorizations=false&revokeTokens=true",
			WantStatus: fiber.StatusOK,
			WantBody:   "false,true",
		},
		{
			Name:        "invalid bool",
			ContentType: contentTypeApplicationJSON,
			Body:        `{"revokeTokens":"abc"}`,
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
			req := httptest.NewRequest(http.MethodDelete, "/", strings.NewReader(testCase.Body))
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

func TestParseAdminOAuthClientTrendBucket(t *testing.T) {
	bucket, err := parseAdminOAuthClientTrendBucket("")
	if err != nil {
		t.Fatalf("parseAdminOAuthClientTrendBucket returned error: %v", err)
	}
	if bucket != defaultAdminOAuthClientTrendBucket {
		t.Fatalf("bucket = %q, want %q", bucket, defaultAdminOAuthClientTrendBucket)
	}

	bucket, err = parseAdminOAuthClientTrendBucket("day")
	if err != nil {
		t.Fatalf("parseAdminOAuthClientTrendBucket returned error: %v", err)
	}
	if bucket != adminOAuthClientTrendBucketDay {
		t.Fatalf("bucket = %q, want %q", bucket, adminOAuthClientTrendBucketDay)
	}

	if _, err := parseAdminOAuthClientTrendBucket("minute"); err == nil {
		t.Fatalf("expected invalid bucket to fail")
	}
}

func TestParseAdminOAuthClientStatisticsFilters(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		_, err := parseAdminOAuthClientStatisticsFilters(c, time.Date(2026, time.March, 8, 12, 0, 0, 0, time.UTC))
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.SendStatus(fiberErr.Code)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	t.Run("valid filters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?from=2026-03-08T00:00:00Z&to=2026-03-08T12:00:00Z&bucket=day", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
	})

	t.Run("invalid bucket", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?bucket=minute", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("invalid time range", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?from=2026-03-08T12:00:00Z&to=2026-03-08T11:00:00Z", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}

func TestBuildAdminOAuthClientTrendPoints(t *testing.T) {
	from := time.Date(2026, time.March, 8, 0, 15, 0, 0, time.UTC)
	to := time.Date(2026, time.March, 8, 3, 45, 0, 0, time.UTC)

	authorizationTimes := []time.Time{
		time.Date(2026, time.March, 8, 0, 20, 0, 0, time.UTC),
		time.Date(2026, time.March, 8, 1, 10, 0, 0, time.UTC),
		time.Date(2026, time.March, 8, 1, 50, 0, 0, time.UTC),
	}
	tokenTimes := []time.Time{
		time.Date(2026, time.March, 8, 1, 5, 0, 0, time.UTC),
		time.Date(2026, time.March, 8, 3, 0, 0, 0, time.UTC),
	}

	points := buildAdminOAuthClientTrendPoints(from, to, adminOAuthClientTrendBucketHour, authorizationTimes, tokenTimes)
	if len(points) != 4 {
		t.Fatalf("len(points) = %d, want 4", len(points))
	}

	if points[0].BucketStart.Hour() != 0 || points[0].AuthorizationCreated != 1 || points[0].TokenIssued != 0 {
		t.Fatalf("unexpected points[0]: %#v", points[0])
	}
	if points[1].BucketStart.Hour() != 1 || points[1].AuthorizationCreated != 2 || points[1].TokenIssued != 1 {
		t.Fatalf("unexpected points[1]: %#v", points[1])
	}
	if points[2].BucketStart.Hour() != 2 || points[2].AuthorizationCreated != 0 || points[2].TokenIssued != 0 {
		t.Fatalf("unexpected points[2]: %#v", points[2])
	}
	if points[3].BucketStart.Hour() != 3 || points[3].AuthorizationCreated != 0 || points[3].TokenIssued != 1 {
		t.Fatalf("unexpected points[3]: %#v", points[3])
	}
}

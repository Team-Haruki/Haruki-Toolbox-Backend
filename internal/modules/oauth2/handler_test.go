package oauth2

import (
	"encoding/base64"
	"errors"
	"haruki-suite/utils/database/postgresql"
	"io"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestIsFormURLEncodedContentType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		contentType string
		want        bool
	}{
		{
			name:        "plain form",
			contentType: "application/x-www-form-urlencoded",
			want:        true,
		},
		{
			name:        "form with charset",
			contentType: "application/x-www-form-urlencoded; charset=UTF-8",
			want:        true,
		},
		{
			name:        "json body",
			contentType: "application/json",
			want:        false,
		},
		{
			name:        "empty",
			contentType: "",
			want:        false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := isFormURLEncodedContentType(tc.contentType)
			if got != tc.want {
				t.Fatalf("isFormURLEncodedContentType(%q) = %v, want %v", tc.contentType, got, tc.want)
			}
		})
	}
}

func TestParseOAuthFormBody(t *testing.T) {
	t.Parallel()

	t.Run("valid body", func(t *testing.T) {
		t.Parallel()
		formValues, err := parseOAuthFormBody([]byte("grant_type=refresh_token&scope=user%3Aread+bindings%3Aread&code=abc%2B123"))
		if err != nil {
			t.Fatalf("parseOAuthFormBody returned error: %v", err)
		}
		if got := formValues.Get("grant_type"); got != "refresh_token" {
			t.Fatalf("grant_type = %q, want %q", got, "refresh_token")
		}
		if got := formValues.Get("scope"); got != "user:read bindings:read" {
			t.Fatalf("scope = %q, want %q", got, "user:read bindings:read")
		}
		if got := formValues.Get("code"); got != "abc+123" {
			t.Fatalf("code = %q, want %q", got, "abc+123")
		}
	})

	t.Run("invalid escape", func(t *testing.T) {
		t.Parallel()
		if _, err := parseOAuthFormBody([]byte("token=%ZZ")); err == nil {
			t.Fatalf("parseOAuthFormBody should fail on invalid percent-encoding")
		}
	})
}

func TestBuildRedirectURL(t *testing.T) {
	t.Parallel()

	redirect := buildRedirectURL("https://example.com/cb?foo=bar", "abc", map[string]string{
		"code":  "xyz",
		"extra": "1",
	})
	u, err := url.Parse(redirect)
	if err != nil {
		t.Fatalf("buildRedirectURL returned invalid url: %v", err)
	}
	query := u.Query()
	if query.Get("foo") != "bar" {
		t.Fatalf("foo = %q, want bar", query.Get("foo"))
	}
	if query.Get("state") != "abc" {
		t.Fatalf("state = %q, want abc", query.Get("state"))
	}
	if query.Get("code") != "xyz" {
		t.Fatalf("code = %q, want xyz", query.Get("code"))
	}
	if query.Get("extra") != "1" {
		t.Fatalf("extra = %q, want 1", query.Get("extra"))
	}
}

func TestRespondOAuthErrorWithChallenge(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		return respondOAuthError(c, oauthErrorResponse{
			Status:               fiber.StatusUnauthorized,
			Code:                 "invalid_client",
			Description:          "bad client",
			BasicChallengeNeeded: true,
		})
	})

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
	}
	if got := resp.Header.Get("WWW-Authenticate"); !strings.Contains(got, "Basic") {
		t.Fatalf("WWW-Authenticate = %q, want Basic challenge", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if !strings.Contains(string(body), "\"error\":\"invalid_client\"") {
		t.Fatalf("unexpected body: %s", string(body))
	}
}

func TestParseBasicAuthorizationValue(t *testing.T) {
	t.Parallel()

	t.Run("valid basic auth", func(t *testing.T) {
		t.Parallel()
		rawID := url.QueryEscape("client/id")
		rawSecret := url.QueryEscape("sec:ret")
		cred := rawID + ":" + rawSecret
		header := "Basic " + base64.StdEncoding.EncodeToString([]byte(cred))

		clientID, clientSecret, presented, err := parseBasicAuthorizationValue(header)
		if err != nil {
			t.Fatalf("parseBasicAuthorizationValue returned error: %v", err)
		}
		if !presented {
			t.Fatalf("presented = false, want true")
		}
		if clientID != "client/id" {
			t.Fatalf("clientID = %q, want %q", clientID, "client/id")
		}
		if clientSecret != "sec:ret" {
			t.Fatalf("clientSecret = %q, want %q", clientSecret, "sec:ret")
		}
	})

	t.Run("no auth header", func(t *testing.T) {
		t.Parallel()
		_, _, presented, err := parseBasicAuthorizationValue("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if presented {
			t.Fatalf("presented = true, want false")
		}
	})

	t.Run("unsupported scheme", func(t *testing.T) {
		t.Parallel()
		_, _, presented, err := parseBasicAuthorizationValue("Bearer abc")
		if err == nil {
			t.Fatalf("expected error for unsupported auth scheme")
		}
		if !presented {
			t.Fatalf("presented = false, want true")
		}
	})

	t.Run("invalid basic payload", func(t *testing.T) {
		t.Parallel()
		_, _, presented, err := parseBasicAuthorizationValue("Basic !!!!")
		if err == nil {
			t.Fatalf("expected error for invalid basic payload")
		}
		if !presented {
			t.Fatalf("presented = false, want true")
		}
	})
}

func TestIsScopeSubset(t *testing.T) {
	t.Parallel()

	granted := []string{"user:read", "bindings:read", "game-data:read"}
	if !isScopeSubset([]string{"user:read", "bindings:read"}, granted) {
		t.Fatalf("expected requested scopes to be subset")
	}
	if isScopeSubset([]string{"user:write"}, granted) {
		t.Fatalf("expected requested scopes not to be subset")
	}
}

func TestParseScopeList(t *testing.T) {
	t.Parallel()

	got := parseScopeList(" user:read   bindings:read\tgame-data:read ")
	want := []string{"user:read", "bindings:read", "game-data:read"}
	if len(got) != len(want) {
		t.Fatalf("parseScopeList length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseScopeList[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestExtractClientAuthentication(t *testing.T) {
	t.Parallel()

	buildBasic := func(clientID, clientSecret string) string {
		cred := url.QueryEscape(clientID) + ":" + url.QueryEscape(clientSecret)
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(cred))
	}

	invoke := func(t *testing.T, authHeader string, formValues url.Values) (oauthClientAuthentication, *oauthErrorResponse) {
		t.Helper()

		app := fiber.New()
		var gotAuth oauthClientAuthentication
		var gotErr *oauthErrorResponse
		app.Post("/", func(c fiber.Ctx) error {
			gotAuth, gotErr = extractClientAuthentication(c, formValues)
			return c.SendStatus(fiber.StatusNoContent)
		})

		req := httptest.NewRequest(fiber.MethodPost, "/", nil)
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
		return gotAuth, gotErr
	}

	t.Run("body credentials only", func(t *testing.T) {
		t.Parallel()
		auth, errResp := invoke(t, "", url.Values{
			"client_id":     {"body-client"},
			"client_secret": {"body-secret"},
		})
		if errResp != nil {
			t.Fatalf("unexpected oauth error: %+v", *errResp)
		}
		if auth.ClientID != "body-client" || auth.ClientSecret != "body-secret" {
			t.Fatalf("unexpected auth parsed: %+v", auth)
		}
	})

	t.Run("basic credentials only", func(t *testing.T) {
		t.Parallel()
		auth, errResp := invoke(t, buildBasic("basic/client", "sec:ret"), nil)
		if errResp != nil {
			t.Fatalf("unexpected oauth error: %+v", *errResp)
		}
		if auth.ClientID != "basic/client" || auth.ClientSecret != "sec:ret" {
			t.Fatalf("unexpected auth parsed: %+v", auth)
		}
	})

	t.Run("basic and body together", func(t *testing.T) {
		t.Parallel()
		_, errResp := invoke(t, buildBasic("basic-client", "basic-secret"), url.Values{
			"client_id": {"body-client"},
		})
		if errResp == nil {
			t.Fatalf("expected oauth error response")
		}
		if errResp.Status != fiber.StatusBadRequest || errResp.Code != "invalid_request" {
			t.Fatalf("unexpected oauth error: %+v", *errResp)
		}
	})

	t.Run("invalid basic payload", func(t *testing.T) {
		t.Parallel()
		_, errResp := invoke(t, "Basic !!!!", nil)
		if errResp == nil {
			t.Fatalf("expected oauth error response")
		}
		if errResp.Status != fiber.StatusUnauthorized || errResp.Code != "invalid_client" || !errResp.BasicChallengeNeeded {
			t.Fatalf("unexpected oauth error: %+v", *errResp)
		}
	})
}

func TestBuildConsentDeniedRedirectURL(t *testing.T) {
	t.Parallel()

	t.Run("registered redirect URI", func(t *testing.T) {
		t.Parallel()

		got, err := buildConsentDeniedRedirectURL(
			"https://example.com/callback",
			"state-1",
			[]string{"https://example.com/callback"},
		)
		if err != nil {
			t.Fatalf("buildConsentDeniedRedirectURL returned error: %v", err)
		}
		parsed, err := url.Parse(got)
		if err != nil {
			t.Fatalf("redirect url parse failed: %v", err)
		}
		if parsed.Query().Get("error") != "access_denied" {
			t.Fatalf("error = %q, want access_denied", parsed.Query().Get("error"))
		}
		if parsed.Query().Get("state") != "state-1" {
			t.Fatalf("state = %q, want state-1", parsed.Query().Get("state"))
		}
	})

	t.Run("unregistered redirect URI", func(t *testing.T) {
		t.Parallel()

		if _, err := buildConsentDeniedRedirectURL(
			"https://evil.example/callback",
			"state-1",
			[]string{"https://example.com/callback"},
		); err == nil {
			t.Fatalf("expected error for unregistered redirect URI")
		}
	})
}

func TestShouldCreateAuthorizationOnLookupErr(t *testing.T) {
	t.Parallel()

	t.Run("no error means use existing", func(t *testing.T) {
		t.Parallel()

		create, err := shouldCreateAuthorizationOnLookupErr(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if create {
			t.Fatalf("create = true, want false")
		}
	})

	t.Run("not found means create", func(t *testing.T) {
		t.Parallel()

		create, err := shouldCreateAuthorizationOnLookupErr(new(postgresql.NotFoundError))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !create {
			t.Fatalf("create = false, want true")
		}
	})

	t.Run("other error should be returned", func(t *testing.T) {
		t.Parallel()

		sourceErr := errors.New("db unavailable")
		create, err := shouldCreateAuthorizationOnLookupErr(sourceErr)
		if create {
			t.Fatalf("create = true, want false")
		}
		if !errors.Is(err, sourceErr) {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

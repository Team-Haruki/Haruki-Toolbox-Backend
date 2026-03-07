package oauth2

import (
	"encoding/base64"
	"net/url"
	"testing"
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

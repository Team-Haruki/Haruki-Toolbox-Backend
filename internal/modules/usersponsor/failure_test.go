package usersponsor

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
)

func TestOAuthExchangeRejectsUnsafeProviderResponses(t *testing.T) {
	cfg := OAuthConfig{ClientID: "app", ClientSecret: "secret", RedirectURI: "https://example.test/user/afdian/callback"}
	for _, tc := range []struct {
		name, body string
		code       int
	}{
		{"server error", "private-provider-error", 503}, {"redirect", "", 302},
		{"malformed JSON", "not json", 200}, {"missing identity", `{"ec":200,"data":{}}`, 200},
		{"oversized body", strings.Repeat("x", 65537), 200},
		{"oversized identity", `{"ec":200,"data":{"user_id":"` + strings.Repeat("x", 129) + `"}}`, 200},
		{"invalid identity", `{"ec":200,"data":{"user_id":"alice\nbob"}}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newOAuthHTTPClient()
			client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})
			id, err := exchangeCode(t.Context(), client, cfg, "code")
			if id != "" || !errors.Is(err, errOAuthExchange) {
				t.Fatalf("unsafe exchange: %q %v", id, err)
			}
		})
	}
	client := newOAuthHTTPClient()
	client.Transport = roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("secret-provider-details") })
	if id, err := exchangeCode(t.Context(), client, cfg, "code"); id != "" || !errors.Is(err, errOAuthExchange) {
		t.Fatal("network error exposed upstream details")
	}
	var calls int
	client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://untrusted.test/steal"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if _, err := exchangeCode(t.Context(), client, cfg, "code"); !errors.Is(err, errOAuthExchange) || calls != 1 {
		t.Fatal("followed an authorization credential redirect")
	}
}

func TestOAuthUsesActualKratosSessionWhenProxySessionIsAbsent(t *testing.T) {
	e := newEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sessions/whoami" || r.Header.Get("X-Session-Token") != "kratos-session-token" {
			w.WriteHeader(401)
			return
		}
		_, _ = io.WriteString(w, `{"id":"resolved-session","active":true}`)
	}))
	defer server.Close()
	e.h.api.SessionHandler = &api.SessionHandler{KratosPublicURL: server.URL, KratosSessionHeader: "X-Session-Token"}
	req := httptest.NewRequest("POST", "/api/user/alice/afdian/authorize", nil)
	req.Header.Set("X-Test-User", "alice")
	req.Header.Set("X-Session-Token", "kratos-session-token")
	resp, err := e.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("Kratos session rejected: %d", resp.StatusCode)
	}
	status, _ := e.request(t, "POST", "/api/user/alice/afdian/authorize", "alice", "", nil)
	if status != 401 {
		t.Fatal("missing session started OAuth")
	}
	status, _ = e.request(t, "POST", "/api/user/alice/afdian/callback", "alice", "", map[string]string{"code": "code", "state": strings.Repeat("x", 43)})
	if status != 401 || e.exchanges.Load() != 0 {
		t.Fatal("missing session redeemed OAuth")
	}
}

func TestVerificationRefusesUnavailableRateLimitStore(t *testing.T) {
	e := newEnv(t)
	status, _ := e.request(t, "POST", "/api/user/alice/afdian/verify-order", "alice", "session", map[string]string{"orderNumber": testOrderNumber})
	if status != 503 {
		t.Fatal("unconfigured orders were enabled")
	}
	calls := mockOrders(t, e, nil, 200, 200)
	manager := e.h.api.DBManager.Redis
	for _, nilManager := range []bool{true, false} {
		if nilManager {
			e.h.api.DBManager.Redis = nil
		} else {
			e.h.api.DBManager.Redis = manager
			_ = manager.Redis.Close()
		}
		for _, path := range []string{"authorize", "callback", "verify-order"} {
			status, _ = e.request(t, "POST", "/api/user/alice/afdian/"+path, "alice", "session", map[string]string{"orderNumber": testOrderNumber})
			if status != 503 {
				t.Fatalf("%s bypassed unavailable limits: %d", path, status)
			}
		}
	}
	if calls.Load() != 0 || e.exchanges.Load() != 0 {
		t.Fatal("unlimited provider call escaped")
	}
}

func TestInvalidOAuthCallbackNeverCallsProvider(t *testing.T) {
	e := newEnv(t)
	for _, payload := range []any{"invalid body", map[string]string{"code": "", "state": strings.Repeat("x", 43)}, map[string]string{"code": strings.Repeat("x", 2049), "state": strings.Repeat("x", 43)}, map[string]string{"code": "code", "state": "short"}} {
		status, _ := e.request(t, "POST", "/api/user/alice/afdian/callback", "alice", "session", payload)
		if status != 400 {
			t.Fatalf("invalid callback accepted: %d", status)
		}
	}
	e.h.oauth = OAuthConfig{}
	status, _ := e.request(t, "POST", "/api/user/alice/afdian/callback", "alice", "session", nil)
	if status != 503 || e.exchanges.Load() != 0 {
		t.Fatal("disabled callback attempted exchange")
	}
}

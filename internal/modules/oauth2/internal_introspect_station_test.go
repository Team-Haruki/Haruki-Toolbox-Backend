package oauth2

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

// This file replays Sekai Station's RFC 7662 introspection client
// (station-backend internal/auth/oauth/introspection.go and verifier.go)
// against the real endpoint over HTTP: the request construction, the
// response checks and authorize() are copied as they are there, with
// encoding/json v1 as Station uses it. Keep it in step with Station.

var (
	errStationUnavailable  = errors.New("station: unavailable")
	errStationUnauthorized = errors.New("station: unauthorized")
	errStationForbidden    = errors.New("station: forbidden")
)

// stationOAuthConfig is the part of Station's settings.OAuth the
// introspection mode reads.
type stationOAuthConfig struct {
	Issuer           string
	Audience         string
	IntrospectionURL string
	ClientID         string
	RequiredScopes   []string
}

type stationAudience []string

func (a *stationAudience) UnmarshalJSON(data []byte) error {
	var s string
	if json.Unmarshal(data, &s) == nil {
		*a = []string{s}
		return nil
	}
	var values []string
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	*a = values
	return nil
}

func (a stationAudience) contains(want string) bool {
	for _, value := range a {
		if value == want {
			return true
		}
	}
	return false
}

type stationClaims struct {
	Subject   string          `json:"sub"`
	Issuer    string          `json:"iss"`
	Audience  stationAudience `json:"aud"`
	Expiry    int64           `json:"exp"`
	NotBefore int64           `json:"nbf"`
	Scope     string          `json:"scope"`
	SCP       json.RawMessage `json:"scp"`
}

func stationAuthorize(c stationClaims, required []string) (string, error) {
	if strings.TrimSpace(c.Subject) == "" || len(c.Subject) > 128 {
		return "", errStationUnauthorized
	}
	scopes := strings.Fields(c.Scope)
	if len(c.SCP) != 0 {
		var value string
		if json.Unmarshal(c.SCP, &value) == nil {
			scopes = append(scopes, strings.Fields(value)...)
		} else {
			var values []string
			if json.Unmarshal(c.SCP, &values) != nil {
				return "", errStationUnauthorized
			}
			scopes = append(scopes, values...)
		}
	}
	for _, wanted := range required {
		found := false
		for _, scope := range scopes {
			if scope == wanted {
				found = true
				break
			}
		}
		if !found {
			return "", errStationForbidden
		}
	}
	return c.Subject, nil
}

func stationIntrospect(ctx context.Context, client *http.Client, cfg stationOAuthConfig, secret, token string) (string, error) {
	form := url.Values{"token": {token}, "token_type_hint": {"access_token"}}
	r, err := http.NewRequestWithContext(ctx, "POST", cfg.IntrospectionURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", errStationUnavailable
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "application/json")
	r.SetBasicAuth(cfg.ClientID, secret)
	res, err := client.Do(r)
	if err != nil {
		return "", errStationUnavailable
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return "", errStationUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return "", errStationUnavailable
	}
	var result struct {
		Active bool `json:"active"`
		stationClaims
	}
	if json.Unmarshal(data, &result) != nil {
		return "", errStationUnavailable
	}
	if !result.Active || (result.Expiry != 0 && result.Expiry <= time.Now().Unix()) || (result.NotBefore != 0 && result.NotBefore > time.Now().Unix()) || !result.Audience.contains(cfg.Audience) || (result.Issuer != "" && result.Issuer != cfg.Issuer) {
		return "", errStationUnauthorized
	}
	return stationAuthorize(result.stationClaims, cfg.RequiredScopes)
}

// serveIntrospectTestEnv serves the env's app on a loopback port and returns
// the introspection URL.
func serveIntrospectTestEnv(t *testing.T, env *introspectTestEnv) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = env.app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true})
	}()
	t.Cleanup(func() {
		_ = env.app.Shutdown()
		<-done
	})
	return "http://" + ln.Addr().String() + InternalIntrospectPath
}

func TestInternalIntrospectServesStationClient(t *testing.T) {
	env := newIntrospectTestEnv(t)
	cfg := stationOAuthConfig{
		Issuer:           "https://toolbox-api-direct.haruki.seiunx.com",
		Audience:         "station",
		IntrospectionURL: serveIntrospectTestEnv(t, env),
		ClientID:         "station",
		RequiredScopes:   []string{"station:room:write"},
	}
	// Station's verifier.New client: 5 s timeout, redirects not followed.
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ctx := context.Background()

	env.hydra.setToken("ory_at_noStationScope0123456789", activeTokenBody(testIntrospectIdentityID, testIntrospectClientID, "user:read offline_access"))
	env.hydra.setToken("ory_at_legacySubject0123456789", activeTokenBody("legacy-user", testIntrospectClientID, "station:room:write"))

	cases := []struct {
		name, secret, token string
		mutate              func(*stationOAuthConfig)
		wantSubject         string
		wantErr             error
	}{
		{name: "accepted", secret: testInternalToken, token: testActiveAccessToken, wantSubject: testIntrospectUserID},
		{name: "legacy subject", secret: testInternalToken, token: "ory_at_legacySubject0123456789", wantSubject: "legacy-user"},
		{name: "missing scope", secret: testInternalToken, token: "ory_at_noStationScope0123456789", wantErr: errStationForbidden},
		{name: "inactive", secret: testInternalToken, token: "ory_at_unknownToken0123456789", wantErr: errStationUnauthorized},
		{name: "other audience", secret: testInternalToken, token: testActiveAccessToken, mutate: func(c *stationOAuthConfig) { c.Audience = "mafuyu" }, wantErr: errStationUnauthorized},
		{name: "wrong secret", secret: "wrong-internal-token-value", token: testActiveAccessToken, wantErr: errStationUnavailable},
		{name: "wrong client", secret: testInternalToken, token: testActiveAccessToken, mutate: func(c *stationOAuthConfig) { c.ClientID = "mafuyu" }, wantErr: errStationUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			caseCfg := cfg
			if tc.mutate != nil {
				tc.mutate(&caseCfg)
			}
			subject, err := stationIntrospect(ctx, client, caseCfg, tc.secret, tc.token)
			if !errors.Is(err, tc.wantErr) || subject != tc.wantSubject {
				t.Fatalf("stationIntrospect = %q, %v; want %q, %v", subject, err, tc.wantSubject, tc.wantErr)
			}
		})
	}
}

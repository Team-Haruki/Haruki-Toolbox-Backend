package oauth2

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"

	_ "github.com/mattn/go-sqlite3"
)

func newIntrospectAccessTokenTestDB(t *testing.T) *postgresql.Client {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", strings.ReplaceAll(t.Name(), "/", "-"))
	db := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	if _, err := db.User.Create().SetID("u-kratos").SetName("kratos").SetEmail("kratos@example.com").
		SetKratosIdentityID("kid-1").Save(ctx); err != nil {
		t.Fatalf("create kratos user: %v", err)
	}
	if _, err := db.User.Create().SetID("u-legacy").SetName("legacy").SetEmail("legacy@example.com").
		Save(ctx); err != nil {
		t.Fatalf("create legacy user: %v", err)
	}
	if _, err := db.User.Create().SetID("u-banned").SetName("banned").SetEmail("banned@example.com").
		SetKratosIdentityID("kid-banned").SetBanned(true).Save(ctx); err != nil {
		t.Fatalf("create banned user: %v", err)
	}
	return db
}

// newIntrospectAccessTokenHydra answers /admin/oauth2/introspect with the body
// registered for the presented token; unknown tokens are inactive.
func newIntrospectAccessTokenHydra(t *testing.T, bodies map[string]string, calls *atomic.Int32) *HydraConfig {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/admin/oauth2/introspect" || r.ParseForm() != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if hint := r.PostForm.Get("token_type_hint"); hint != "access_token" {
			t.Errorf("token_type_hint = %q, want access_token", hint)
		}
		token := r.PostForm.Get("token")
		if token == "token-hydra-down" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"server_error"}`))
			return
		}
		body, ok := bodies[token]
		if !ok {
			body = `{"active":false}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return NewHydraConfig(HydraConfigOptions{AdminURL: server.URL, RequestTimeout: 5 * time.Second})
}

func TestIntrospectAccessTokenAppliesBearerRules(t *testing.T) {
	now := time.Now().Unix()
	exp := now + 3600
	iat := now - 60
	bodies := map[string]string{
		"token-full":           fmt.Sprintf(`{"active":true,"sub":"kid-1","client_id":"client-a","scope":"user:read game-data:read bindings:read","token_use":"access_token","exp":%d,"iat":%d,"ext":{"device_label":" Desk PC "}}`, exp, iat),
		"token-legacy-subject": `{"active":true,"sub":"u-legacy","client_id":"client-a","scope":"user:read","token_use":"access_token"}`,
		"token-username-only":  `{"active":true,"username":"kid-1","client_id":"client-a","scope":"user:read","token_use":"access_token"}`,
		"token-no-scope":       `{"active":true,"sub":"kid-1","client_id":"client-a","token_use":"access_token"}`,
		"token-refresh":        `{"active":true,"sub":"kid-1","client_id":"client-a","scope":"user:read","token_use":"refresh_token"}`,
		"token-no-subject":     `{"active":true,"client_id":"client-a","scope":"user:read","token_use":"access_token"}`,
		"token-expired":        fmt.Sprintf(`{"active":true,"sub":"kid-1","client_id":"client-a","token_use":"access_token","exp":%d}`, now-10),
		"token-not-yet-valid":  fmt.Sprintf(`{"active":true,"sub":"kid-1","client_id":"client-a","token_use":"access_token","nbf":%d}`, now+600),
		"token-unknown-user":   `{"active":true,"sub":"kid-nobody","client_id":"client-a","token_use":"access_token"}`,
		"token-banned-user":    `{"active":true,"sub":"kid-banned","client_id":"client-a","token_use":"access_token"}`,
		"token-no-client":      `{"active":true,"sub":"kid-1","token_use":"access_token"}`,
		"token-client-off":     `{"active":true,"sub":"kid-1","client_id":"client-off","token_use":"access_token"}`,
		"token-client-error":   `{"active":true,"sub":"kid-1","client_id":"client-error","token_use":"access_token"}`,
	}
	var calls atomic.Int32
	hydraConfig := newIntrospectAccessTokenHydra(t, bodies, &calls)
	db := newIntrospectAccessTokenTestDB(t)
	checker := func(_ context.Context, clientID string) (bool, error) {
		switch clientID {
		case "client-off":
			return false, nil
		case "client-error":
			return false, errors.New("client store down")
		default:
			return true, nil
		}
	}
	ctx := context.Background()

	t.Run("active token maps every field", func(t *testing.T) {
		got, err := IntrospectAccessToken(ctx, hydraConfig, db, "token-full", checker)
		if err != nil {
			t.Fatalf("IntrospectAccessToken error: %v", err)
		}
		want := AccessTokenIntrospection{
			Active:      true,
			UserID:      "u-kratos",
			ClientID:    "client-a",
			Scopes:      []string{"user:read", "game-data:read", "bindings:read"},
			Exp:         exp,
			Iat:         iat,
			DeviceLabel: "Desk PC",
		}
		if got.Active != want.Active || got.UserID != want.UserID || got.ClientID != want.ClientID ||
			got.Exp != want.Exp || got.Iat != want.Iat || got.DeviceLabel != want.DeviceLabel ||
			!slices.Equal(got.Scopes, want.Scopes) {
			t.Fatalf("IntrospectAccessToken = %+v, want %+v", got, want)
		}
	})

	t.Run("legacy local user id subject stays valid", func(t *testing.T) {
		got, err := IntrospectAccessToken(ctx, hydraConfig, db, "token-legacy-subject", checker)
		if err != nil || !got.Active || got.UserID != "u-legacy" {
			t.Fatalf("IntrospectAccessToken = %+v, %v; want active u-legacy", got, err)
		}
	})

	t.Run("username is the subject fallback", func(t *testing.T) {
		got, err := IntrospectAccessToken(ctx, hydraConfig, db, "token-username-only", checker)
		if err != nil || !got.Active || got.UserID != "u-kratos" {
			t.Fatalf("IntrospectAccessToken = %+v, %v; want active u-kratos", got, err)
		}
	})

	t.Run("missing scope yields an empty non-nil list", func(t *testing.T) {
		got, err := IntrospectAccessToken(ctx, hydraConfig, db, "token-no-scope", checker)
		if err != nil || !got.Active {
			t.Fatalf("IntrospectAccessToken = %+v, %v; want active", got, err)
		}
		if got.Scopes == nil || len(got.Scopes) != 0 {
			t.Fatalf("Scopes = %#v, want empty non-nil slice", got.Scopes)
		}
		if got.DeviceLabel != "" {
			t.Fatalf("DeviceLabel = %q, want empty without ext", got.DeviceLabel)
		}
	})

	// Every reason the bearer middleware answers 401 is an inactive token,
	// never an error, and never leaks a field.
	for _, token := range []string{
		"token-inactive",
		"token-refresh",
		"token-no-subject",
		"token-expired",
		"token-not-yet-valid",
		"token-unknown-user",
		"token-banned-user",
		"token-no-client",
		"token-client-off",
	} {
		t.Run("inactive "+token, func(t *testing.T) {
			got, err := IntrospectAccessToken(ctx, hydraConfig, db, token, checker)
			if err != nil {
				t.Fatalf("IntrospectAccessToken(%s) error: %v", token, err)
			}
			if got.Active || got.UserID != "" || got.ClientID != "" || got.Scopes != nil || got.Exp != 0 || got.Iat != 0 || got.DeviceLabel != "" {
				t.Fatalf("IntrospectAccessToken(%s) = %+v, want the zero value", token, got)
			}
		})
	}

	// An unknown answer must surface as an error so the caller fails closed
	// instead of reporting the token inactive or active.
	for token, wantMessage := range map[string]string{
		"token-hydra-down":   "oauth2 introspection unavailable: status 503",
		"token-client-error": "oauth2 client validation unavailable",
	} {
		t.Run("error "+token, func(t *testing.T) {
			got, err := IntrospectAccessToken(ctx, hydraConfig, db, token, checker)
			if err == nil {
				t.Fatalf("IntrospectAccessToken(%s) = %+v, want an error", token, got)
			}
			if !strings.Contains(err.Error(), wantMessage) {
				t.Fatalf("error = %q, want it to contain %q", err, wantMessage)
			}
			if got.Active {
				t.Fatalf("IntrospectAccessToken(%s) reported active alongside an error", token)
			}
		})
	}
}

func TestIntrospectAccessTokenBlankTokenSkipsHydra(t *testing.T) {
	var calls atomic.Int32
	hydraConfig := newIntrospectAccessTokenHydra(t, nil, &calls)
	db := newIntrospectAccessTokenTestDB(t)
	checker := func(context.Context, string) (bool, error) {
		t.Fatal("client checker called for a blank token")
		return false, nil
	}
	for _, token := range []string{"", "   ", "\t\n"} {
		got, err := IntrospectAccessToken(context.Background(), hydraConfig, db, token, checker)
		if err != nil || got.Active {
			t.Fatalf("IntrospectAccessToken(%q) = %+v, %v; want inactive and nil", token, got, err)
		}
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("Hydra introspection called %d times for blank tokens, want 0", n)
	}
}

func TestIntrospectAccessTokenRequiresClientCheck(t *testing.T) {
	db := newIntrospectAccessTokenTestDB(t)
	if _, err := IntrospectAccessToken(context.Background(), NewHydraConfig(HydraConfigOptions{}), db, "ory_at_x", nil); err == nil {
		t.Fatal("introspection without a client active checker answered")
	}
}

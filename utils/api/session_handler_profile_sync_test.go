package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/enttest"
	userSchema "haruki-suite/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

func newSessionHandlerTestDB(t *testing.T) *postgresql.Client {
	t.Helper()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name())
	db := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func TestVerifySessionTokenAuthProxySyncsResolvedUserProfile(t *testing.T) {
	db := newSessionHandlerTestDB(t)
	ctx := context.Background()

	_, err := db.User.Create().
		SetID("u-proxy").
		SetName("old-name").
		SetEmail("old@example.com").
		Save(ctx)
	if err != nil {
		t.Fatalf("Create user returned error: %v", err)
	}

	handler := NewSessionHandler(newSessionTestRedisClient(t), "")
	handler.DBClient = db
	handler.ConfigureAuthProxy(true, "X-Auth-Proxy-Secret", "proxy-secret", "X-Kratos-Identity-Id", "X-User-Name", "X-User-Email", "X-User-Email-Verified", "X-User-Id")

	app := fiber.New()
	app.Get("/api/user/:toolbox_user_id/profile", handler.VerifySessionToken, func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/user/u-proxy/profile", nil)
	req.Header.Set("X-Auth-Proxy-Secret", "proxy-secret")
	req.Header.Set("X-User-Id", "u-proxy")
	req.Header.Set("X-Kratos-Identity-Id", "kratos-proxy-1")
	req.Header.Set("X-User-Name", "kratos-display-name")
	req.Header.Set("X-User-Email", "new.email@example.com")
	req.Header.Set("X-User-Email-Verified", "true")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusNoContent {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
	}

	user, err := db.User.Query().Where(userSchema.IDEQ("u-proxy")).Only(ctx)
	if err != nil {
		t.Fatalf("Query user returned error: %v", err)
	}
	if user.Name != "kratos-display-name" {
		t.Fatalf("user.Name = %q, want %q", user.Name, "kratos-display-name")
	}
	if user.Email != "new.email@example.com" {
		t.Fatalf("user.Email = %q, want %q", user.Email, "new.email@example.com")
	}
	if user.KratosIdentityID == nil || *user.KratosIdentityID != "kratos-proxy-1" {
		t.Fatalf("user.KratosIdentityID = %v, want %q", user.KratosIdentityID, "kratos-proxy-1")
	}
}

func TestVerifySessionTokenKratosModeSyncsResolvedUserProfile(t *testing.T) {
	db := newSessionHandlerTestDB(t)
	ctx := context.Background()

	_, err := db.User.Create().
		SetID("u1").
		SetName("legacy-name").
		SetEmail("legacy@example.com").
		Save(ctx)
	if err != nil {
		t.Fatalf("Create user returned error: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sessions/whoami" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("X-Session-Token"); got != "kratos-token" {
			http.Error(w, "missing session token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"active":true,"identity":{"id":"identity-1","traits":{"email":"kratos.user@example.com","name":"kratos-display-name"}}}`))
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", server.URL, "", "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, db)
	handler.KratosIdentityResolver = func(ctx context.Context, identityID string, email string) (string, error) {
		if identityID != "identity-1" {
			t.Fatalf("identityID = %q, want %q", identityID, "identity-1")
		}
		if email != "kratos.user@example.com" {
			t.Fatalf("email = %q, want %q", email, "kratos.user@example.com")
		}
		return "u1", nil
	}

	app := fiber.New()
	app.Get("/api/user/:toolbox_user_id/profile", handler.VerifySessionToken, func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/user/u1/profile", nil)
	req.Header.Set("Authorization", "Bearer kratos-token")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusNoContent {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
	}

	user, err := db.User.Query().Where(userSchema.IDEQ("u1")).Only(ctx)
	if err != nil {
		t.Fatalf("Query user returned error: %v", err)
	}
	if user.Name != "kratos-display-name" {
		t.Fatalf("user.Name = %q, want %q", user.Name, "kratos-display-name")
	}
	if user.Email != "kratos.user@example.com" {
		t.Fatalf("user.Email = %q, want %q", user.Email, "kratos.user@example.com")
	}
	if user.KratosIdentityID == nil || *user.KratosIdentityID != "identity-1" {
		t.Fatalf("user.KratosIdentityID = %v, want %q", user.KratosIdentityID, "identity-1")
	}
}

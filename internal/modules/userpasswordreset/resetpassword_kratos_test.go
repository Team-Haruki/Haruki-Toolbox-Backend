package userpasswordreset

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"haruki-suite/config"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database"
	harukiRedis "haruki-suite/utils/database/redis"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	goredis "github.com/redis/go-redis/v9"
)

func TestHandleSendResetPasswordViaKratos(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error: %v", err)
	}
	defer miniRedis.Close()

	redisClient := goredis.NewClient(&goredis.Options{Addr: miniRedis.Addr()})
	defer func() {
		_ = redisClient.Close()
	}()

	var gotMethod string
	var gotEmail string
	kratosServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/self-service/recovery/api":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"flow-recovery-api-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/self-service/recovery":
			if flow := r.URL.Query().Get("flow"); flow != "flow-recovery-api-1" {
				t.Fatalf("flow = %q, want %q", flow, "flow-recovery-api-1")
			}
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode recovery payload failed: %v", err)
			}
			gotMethod = payload["method"]
			gotEmail = payload["email"]
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"state":"sent_email"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer kratosServer.Close()

	sessionHandler := harukiAPIHelper.NewSessionHandler(redisClient, "local-sign-key")
	sessionHandler.ConfigureIdentityProvider("kratos", kratosServer.URL, "", "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)
	apiHelper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{
			Redis: &harukiRedis.HarukiRedisManager{
				Redis: redisClient,
			},
		},
		SessionHandler: sessionHandler,
	}

	prevBypass := config.Cfg.UserSystem.TurnstileBypass
	config.Cfg.UserSystem.TurnstileBypass = true
	defer func() {
		config.Cfg.UserSystem.TurnstileBypass = prevBypass
	}()

	app := fiber.New()
	app.Post("/api/user/reset-password/send", handleSendResetPassword(apiHelper))

	req := httptest.NewRequest(http.MethodPost, "/api/user/reset-password/send", strings.NewReader(`{"email":"recover@example.com","challengeToken":"bypass"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}
	if gotMethod != "code" {
		t.Fatalf("got recovery method = %q, want %q", gotMethod, "code")
	}
	if gotEmail != "recover@example.com" {
		t.Fatalf("got recovery email = %q, want %q", gotEmail, "recover@example.com")
	}
}

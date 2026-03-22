package userauth

import (
	"context"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database"
	harukiRedis "haruki-suite/utils/database/redis"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	goredis "github.com/redis/go-redis/v9"
)

func newRegisterOTPHelper(t *testing.T) (*harukiAPIHelper.HarukiToolboxRouterHelpers, *harukiRedis.HarukiRedisManager) {
	t.Helper()

	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error: %v", err)
	}
	t.Cleanup(func() {
		srv.Close()
	})

	client := goredis.NewClient(&goredis.Options{Addr: srv.Addr()})
	t.Cleanup(func() {
		_ = client.Close()
	})

	redisManager := &harukiRedis.HarukiRedisManager{Redis: client}
	apiHelper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{Redis: redisManager},
	}
	return apiHelper, redisManager
}

func TestVerifyEmailOTPConsumesCode(t *testing.T) {
	t.Parallel()

	apiHelper, redisManager := newRegisterOTPHelper(t)
	email := "register-otp@example.com"
	code := "123456"
	key := harukiRedis.BuildEmailVerifyKey(email)

	if err := redisManager.SetCache(context.Background(), key, code, 5*time.Minute); err != nil {
		t.Fatalf("seed code key error: %v", err)
	}

	app := fiber.New()
	app.Get("/verify", func(c fiber.Ctx) error {
		ok, err := verifyEmailOTP(c, apiHelper, email, code)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "redis error")
		}
		if !ok {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid or expired verification code")
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/verify", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test first verify error: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != fiber.StatusNoContent {
		t.Fatalf("first verify status = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
	}
	exists, err := redisManager.Redis.Exists(context.Background(), key).Result()
	if err != nil {
		t.Fatalf("exists check error: %v", err)
	}
	if exists != 0 {
		t.Fatalf("expected verification key to be consumed after first verify")
	}

	req = httptest.NewRequest(http.MethodGet, "/verify", nil)
	resp, err = app.Test(req)
	if err != nil {
		t.Fatalf("app.Test second verify error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("second verify status = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
	}
}

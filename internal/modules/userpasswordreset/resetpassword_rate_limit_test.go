package userpasswordreset

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database"
	harukiRedis "haruki-suite/utils/database/redis"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	goredis "github.com/redis/go-redis/v9"
)

func newResetPasswordRateLimitHelper(t *testing.T) *harukiAPIHelper.HarukiToolboxRouterHelpers {
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
	return &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{Redis: redisManager},
	}
}

func TestEnforceResetPasswordSendRateLimit(t *testing.T) {
	t.Parallel()

	apiHelper := newResetPasswordRateLimitHelper(t)
	email := "reset@example.com"
	clientIP := "127.0.0.1"

	app := fiber.New()
	app.Get("/send", func(c fiber.Ctx) error {
		limited, key, message, err := checkResetPasswordSendRateLimit(c, apiHelper, clientIP, email)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "reset service unavailable")
		}
		if limited {
			return respondResetPasswordRateLimited(c, key, message, apiHelper)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	for i := 0; i < resetPasswordSendTargetLimit; i++ {
		req := httptest.NewRequest(http.MethodGet, "/send", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test round %d error: %v", i, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("round %d status = %d, want %d", i, resp.StatusCode, fiber.StatusNoContent)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/send", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test overflow error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("overflow status = %d, want %d", resp.StatusCode, fiber.StatusTooManyRequests)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Fatalf("expected Retry-After header on rate limit response")
	}
}

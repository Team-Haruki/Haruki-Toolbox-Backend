package usersocial

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

func newSocialRateLimitTestHelper(t *testing.T) *harukiAPIHelper.HarukiToolboxRouterHelpers {
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

	return &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{
			Redis: &harukiRedis.HarukiRedisManager{Redis: client},
		},
	}
}

func TestQQMailSendRateLimitPerTarget(t *testing.T) {
	t.Parallel()

	helper := newSocialRateLimitTestHelper(t)
	app := fiber.New()
	app.Get("/send", func(c fiber.Ctx) error {
		limited, key, message, err := checkQQMailSendRateLimit(c, helper, "user-1", "123456")
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "verification service unavailable")
		}
		if limited {
			return respondQQMailRateLimited(c, key, message, helper)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	for i := 0; i < qqMailSendTargetLimit; i++ {
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
		t.Fatalf("expected Retry-After header on QQ rate limit response")
	}
}

func TestQQMailSendRateLimitPerUser(t *testing.T) {
	t.Parallel()

	helper := newSocialRateLimitTestHelper(t)
	app := fiber.New()
	app.Get("/send", func(c fiber.Ctx) error {
		limited, key, message, err := checkQQMailSendRateLimit(c, helper, "user-1", c.Query("qq"))
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "verification service unavailable")
		}
		if limited {
			return respondQQMailRateLimited(c, key, message, helper)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	for i := 0; i < qqMailSendUserLimit; i++ {
		req := httptest.NewRequest(http.MethodGet, "/send?qq=12345"+string(rune('a'+i)), nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test round %d error: %v", i, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("round %d status = %d, want %d", i, resp.StatusCode, fiber.StatusNoContent)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/send?qq=overflow", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test overflow error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("overflow status = %d, want %d", resp.StatusCode, fiber.StatusTooManyRequests)
	}
}

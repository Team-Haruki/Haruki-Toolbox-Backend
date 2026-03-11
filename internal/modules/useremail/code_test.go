package useremail

import (
	"context"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database"
	harukiRedis "haruki-suite/utils/database/redis"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	goredis "github.com/redis/go-redis/v9"
)

func TestGenerateCode(t *testing.T) {
	t.Parallel()

	plain, err := GenerateCode(false)
	if err != nil {
		t.Fatalf("GenerateCode(false) error: %v", err)
	}
	if len(plain) != 6 {
		t.Fatalf("plain code length = %d, want 6", len(plain))
	}
	for _, ch := range plain {
		if ch < '0' || ch > '9' {
			t.Fatalf("plain code contains non-digit: %q", string(ch))
		}
	}

	anti, err := GenerateCode(true)
	if err != nil {
		t.Fatalf("GenerateCode(true) error: %v", err)
	}
	if !strings.Contains(anti, "/") {
		t.Fatalf("anti-censor code should contain slash separators")
	}
	normalized := strings.ReplaceAll(anti, "/", "")
	if len(normalized) != 6 {
		t.Fatalf("normalized anti-censor code length = %d, want 6", len(normalized))
	}
	for _, ch := range normalized {
		if ch < '0' || ch > '9' {
			t.Fatalf("normalized anti-censor code contains non-digit: %q", string(ch))
		}
	}
}

func newEmailVerifyTestHelper(t *testing.T) (*harukiAPIHelper.HarukiToolboxRouterHelpers, *harukiRedis.HarukiRedisManager, context.Context) {
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
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{Redis: redisManager},
	}
	return helper, redisManager, context.Background()
}

func runVerifyEmailHandlerStatus(t *testing.T, helper *harukiAPIHelper.HarukiToolboxRouterHelpers, email, otp string) int {
	t.Helper()

	app := fiber.New()
	app.Post("/verify", func(c fiber.Ctx) error {
		ok, err := VerifyEmailHandler(c, email, otp, helper)
		if err != nil {
			return err
		}
		if !ok {
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/verify", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test error: %v", err)
	}
	t.Cleanup(func() {
		_ = resp.Body.Close()
	})
	return resp.StatusCode
}

func TestVerifyEmailHandlerConsumesCodeAndClearsAttempts(t *testing.T) {
	t.Parallel()

	helper, redisManager, ctx := newEmailVerifyTestHelper(t)
	email := "test@example.com"
	code := "123456"
	verifyKey := harukiRedis.BuildEmailVerifyKey(email)
	attemptKey := harukiRedis.BuildOTPAttemptKey(email)

	if err := redisManager.SetCache(ctx, verifyKey, code, 5*time.Minute); err != nil {
		t.Fatalf("seed verify code error: %v", err)
	}
	if err := redisManager.SetCache(ctx, attemptKey, 2, 5*time.Minute); err != nil {
		t.Fatalf("seed attempt count error: %v", err)
	}

	status := runVerifyEmailHandlerStatus(t, helper, email, code)
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d, want %d", status, fiber.StatusNoContent)
	}

	exists, err := redisManager.Redis.Exists(ctx, verifyKey, attemptKey).Result()
	if err != nil {
		t.Fatalf("exists check error: %v", err)
	}
	if exists != 0 {
		t.Fatalf("verification code and attempt key should be deleted, exists=%d", exists)
	}

	status = runVerifyEmailHandlerStatus(t, helper, email, code)
	if status != fiber.StatusBadRequest {
		t.Fatalf("reused code status = %d, want %d", status, fiber.StatusBadRequest)
	}
}

func TestVerifyEmailHandlerWrongCodeIncrementsAttempts(t *testing.T) {
	t.Parallel()

	helper, redisManager, ctx := newEmailVerifyTestHelper(t)
	email := "wrong@example.com"
	verifyKey := harukiRedis.BuildEmailVerifyKey(email)
	attemptKey := harukiRedis.BuildOTPAttemptKey(email)

	if err := redisManager.SetCache(ctx, verifyKey, "654321", 5*time.Minute); err != nil {
		t.Fatalf("seed verify code error: %v", err)
	}

	status := runVerifyEmailHandlerStatus(t, helper, email, "000000")
	if status != fiber.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, fiber.StatusBadRequest)
	}

	var attempts int
	found, err := redisManager.GetCache(ctx, attemptKey, &attempts)
	if err != nil {
		t.Fatalf("GetCache attemptKey error: %v", err)
	}
	if !found {
		t.Fatalf("attempt key should exist after wrong otp")
	}
	if attempts != 1 {
		t.Fatalf("attempt count = %d, want 1", attempts)
	}
}

func TestEnforceSendEmailRateLimit(t *testing.T) {
	t.Parallel()

	helper, _, _ := newEmailVerifyTestHelper(t)
	email := "rate-limit@example.com"
	clientIP := "127.0.0.1"

	app := fiber.New()
	app.Get("/send", func(c fiber.Ctx) error {
		limited, key, message, err := checkSendEmailRateLimit(c, helper, clientIP, email)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "verification service unavailable")
		}
		if limited {
			return respondEmailSendRateLimited(c, key, message, helper)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	for i := 0; i < emailSendTargetLimit; i++ {
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

func TestResolveVerifyEmailFinalizeOutcome(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name               string
		localMirrorFailed  bool
		sessionClearFailed bool
		wantStatus         int
		wantResult         string
		wantReason         string
	}

	tests := []testCase{
		{
			name:               "local mirror and session clear failed",
			localMirrorFailed:  true,
			sessionClearFailed: true,
			wantStatus:         fiber.StatusInternalServerError,
			wantResult:         harukiAPIHelper.SystemLogResultFailure,
			wantReason:         "local_mirror_and_session_clear_failed",
		},
		{
			name:               "local mirror failed",
			localMirrorFailed:  true,
			sessionClearFailed: false,
			wantStatus:         fiber.StatusInternalServerError,
			wantResult:         harukiAPIHelper.SystemLogResultFailure,
			wantReason:         "local_mirror_failed",
		},
		{
			name:               "session clear failed",
			localMirrorFailed:  false,
			sessionClearFailed: true,
			wantStatus:         fiber.StatusOK,
			wantResult:         harukiAPIHelper.SystemLogResultSuccess,
			wantReason:         "ok_session_clear_failed",
		},
		{
			name:               "all success",
			localMirrorFailed:  false,
			sessionClearFailed: false,
			wantStatus:         fiber.StatusOK,
			wantResult:         harukiAPIHelper.SystemLogResultSuccess,
			wantReason:         "ok",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotStatus, _, gotResult, gotReason := resolveVerifyEmailFinalizeOutcome(tc.localMirrorFailed, tc.sessionClearFailed)
			if gotStatus != tc.wantStatus {
				t.Fatalf("status = %d, want %d", gotStatus, tc.wantStatus)
			}
			if gotResult != tc.wantResult {
				t.Fatalf("result = %q, want %q", gotResult, tc.wantResult)
			}
			if gotReason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", gotReason, tc.wantReason)
			}
		})
	}
}

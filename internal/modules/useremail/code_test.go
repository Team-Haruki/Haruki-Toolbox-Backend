package useremail

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiCloudflare "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/cloudflare"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/smtp"

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

func runVerifyEmailHandlerStatus(t *testing.T, helper *harukiAPIHelper.HarukiToolboxRouterHelpers, ownerID, email, otp string) int {
	t.Helper()

	app := fiber.New()
	app.Post("/verify", func(c fiber.Ctx) error {
		if err := VerifyEmailHandler(c, ownerID, email, otp, helper); err != nil {
			return err
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
	verifyKey := redisManager.KeyBuilder().BuildOwnedEmailVerifyKey("owner", email)
	attemptKey := redisManager.KeyBuilder().BuildOwnedEmailAttemptKey("generation")

	if err := redisManager.Redis.Set(ctx, verifyKey, "generation:"+code, 5*time.Minute).Err(); err != nil {
		t.Fatalf("seed verify code error: %v", err)
	}
	if err := redisManager.SetCache(ctx, attemptKey, 2, 5*time.Minute); err != nil {
		t.Fatalf("seed attempt count error: %v", err)
	}

	status := runVerifyEmailHandlerStatus(t, helper, "owner", email, code)
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

	status = runVerifyEmailHandlerStatus(t, helper, "owner", email, code)
	if status != fiber.StatusBadRequest {
		t.Fatalf("reused code status = %d, want %d", status, fiber.StatusBadRequest)
	}
}

func TestVerifyEmailHandlerWrongCodeIncrementsAttempts(t *testing.T) {
	t.Parallel()

	helper, redisManager, ctx := newEmailVerifyTestHelper(t)
	email := "wrong@example.com"
	verifyKey := redisManager.KeyBuilder().BuildOwnedEmailVerifyKey("owner", email)
	attemptKey := redisManager.KeyBuilder().BuildOwnedEmailAttemptKey("generation")

	if err := redisManager.Redis.Set(ctx, verifyKey, "generation:654321", 5*time.Minute).Err(); err != nil {
		t.Fatalf("seed verify code error: %v", err)
	}

	status := runVerifyEmailHandlerStatus(t, helper, "owner", email, "000000")
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
	app := fiber.New()
	app.Get("/send", func(c fiber.Ctx) error {
		if err := checkSendEmailRateLimit(c, helper, "owner", email); err != nil {
			return err
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

func TestVerificationAttemptsAreScopedToOwnedChallenge(t *testing.T) {
	t.Parallel()
	helper, cache, ctx := newEmailVerifyTestHelper(t)
	const email = "123456@qq.com"
	key := cache.KeyBuilder().BuildOwnedEmailVerifyKey("owner", email)
	if err := cache.Redis.Set(ctx, key, "first:123456", emailCodeTTL).Err(); err != nil {
		t.Fatal(err)
	}
	for range otpVerifyPerCodeLimit + 1 {
		if status := runVerifyEmailHandlerStatus(t, helper, "other", email, "123456"); status != http.StatusBadRequest {
			t.Fatalf("verification by another owner returned %d", status)
		}
	}
	firstAttempts := cache.KeyBuilder().BuildOwnedEmailAttemptKey("first")
	if count := cache.Redis.Exists(ctx, firstAttempts).Val(); count != 0 {
		t.Fatal("verification by another owner spent the owner's attempts")
	}
	for range otpVerifyPerCodeLimit {
		if status := runVerifyEmailHandlerStatus(t, helper, "owner", email, "000000"); status != http.StatusBadRequest {
			t.Fatalf("wrong code returned %d", status)
		}
	}
	if status := runVerifyEmailHandlerStatus(t, helper, "owner", email, "123456"); status != http.StatusBadRequest {
		t.Fatalf("exhausted challenge returned %d", status)
	}
	if err := cache.Redis.Set(ctx, key, "second:123456", emailCodeTTL).Err(); err != nil {
		t.Fatal(err)
	}
	if status := runVerifyEmailHandlerStatus(t, helper, "owner", email, "123456"); status != http.StatusNoContent {
		t.Fatalf("new generation returned %d", status)
	}
}

func TestEmailQuotaDenialPreservesRecipientAllowance(t *testing.T) {
	t.Parallel()
	helper, cache, ctx := newEmailVerifyTestHelper(t)
	app := fiber.New()
	app.Post("/", func(c fiber.Ctx) error {
		return checkSendEmailRateLimit(c, helper, c.Query("owner"), c.Query("email"))
	})
	request := func(ownerID, email string, want int) {
		t.Helper()
		resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/?owner="+ownerID+"&email="+email, nil))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("quota returned %d, want %d", resp.StatusCode, want)
		}
	}
	for i := range emailSendUserLimit {
		request("first", fmt.Sprintf("fixture%d@qq.com", i), http.StatusOK)
	}
	const recipient = "123456@qq.com"
	request("first", recipient, http.StatusTooManyRequests)
	key := cache.KeyBuilder().BuildEmailVerifySendRateLimitTargetKey(recipient)
	if count := cache.Redis.Exists(ctx, key).Val(); count != 0 {
		t.Fatal("rejected sender consumed recipient allowance")
	}
	request("second", recipient, http.StatusOK)
}

type captchaResult struct {
	response *harukiCloudflare.TurnstileResponse
	err      error
}

func (v captchaResult) Verify(context.Context, string, string) (*harukiCloudflare.TurnstileResponse, error) {
	return v.response, v.err
}

func TestEmailSendDoesNotReserveQuotaBeforeCaptchaAdmission(t *testing.T) {
	t.Parallel()
	helper, cache, ctx := newEmailVerifyTestHelper(t)
	const owner, email = "owner", "123456@qq.com"
	for _, tc := range []struct {
		verifier captchaResult
		want     int
	}{
		{captchaResult{response: &harukiCloudflare.TurnstileResponse{Success: false}}, http.StatusBadRequest},
		{captchaResult{err: errors.New("fixture outage")}, http.StatusServiceUnavailable},
	} {
		app := fiber.New()
		app.Post("/", func(c fiber.Ctx) error {
			return SendEmailHandler(c, owner, email, "challenge", helper, tc.verifier)
		})
		resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/", nil))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Fatalf("captcha rejection returned %d, want %d", resp.StatusCode, tc.want)
		}
	}
	keys := cache.KeyBuilder()
	if count := cache.Redis.Exists(ctx, keys.BuildEmailVerifySendRateLimitTargetKey(email), keys.BuildOwnedEmailVerifyKey(owner, email)).Val(); count != 0 {
		t.Fatalf("captcha rejection stored %d quota or code keys", count)
	}
}

func TestUndeliveredEmailChallengeIsDiscarded(t *testing.T) {
	t.Parallel()
	helper, cache, ctx := newEmailVerifyTestHelper(t)
	// An invalid envelope sender fails before the SMTP client dials.
	helper.SMTPClient = &smtp.HarukiSMTPClient{From: "\n"}
	app := fiber.New()
	app.Post("/", func(c fiber.Ctx) error {
		return SendEmailHandler(c, "owner", "123456@qq.com", "valid", helper, captchaResult{
			response: &harukiCloudflare.TurnstileResponse{Success: true},
		})
	})
	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("mail delivery failure returned %d", resp.StatusCode)
	}
	key := cache.KeyBuilder().BuildOwnedEmailVerifyKey("owner", "123456@qq.com")
	if got := cache.Redis.Exists(ctx, key).Val(); got != 0 {
		t.Fatal("an undelivered code remained active")
	}
}

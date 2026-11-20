package useremail

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
		if err := VerifyEmailHandler(c, ownerID, email, otp, helper, func() error { return nil }); err != nil {
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
		if _, err := reserveEmailSend(c, helper, "owner", email); err != nil {
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
		_, err := reserveEmailSend(c, helper, c.Query("owner"), c.Query("email"))
		return err
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

func TestUndeliveredEmailChallengeAndReservationAreReleased(t *testing.T) {
	t.Parallel()
	helper, cache, ctx := newEmailVerifyTestHelper(t)
	const owner, email = "owner", "123456@qq.com"
	key := cache.KeyBuilder().BuildOwnedEmailVerifyKey(owner, email)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	smtpAttempt := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			var count int64
			count, err = cache.Redis.Exists(ctx, key).Result()
			if err == nil && count != 1 {
				err = errors.New("no challenge existed when SMTP transport opened")
			}
			_ = conn.Close()
		}
		smtpAttempt <- err
	}()
	helper.SMTPClient = &smtp.HarukiSMTPClient{Addr: listener.Addr().String(), From: "noreply@example.test", Timeout: 3 * time.Second}
	ipKeys := make(chan string, 1)
	app := fiber.New()
	app.Post("/", func(c fiber.Ctx) error {
		ipKeys <- harukiRedis.BuildEmailVerifySendRateLimitIPKey(c.IP())
		return SendEmailHandler(c, owner, email, "valid", helper, captchaResult{
			response: &harukiCloudflare.TurnstileResponse{Success: true},
		})
	})
	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(raw), "failed to send verification email") {
		t.Fatalf("request did not report a delivery failure, status %d, read error %v", resp.StatusCode, err)
	}
	select {
	case err := <-smtpAttempt:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("the SMTP fixture never got a connection")
	}
	count, err := cache.Redis.Exists(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("an undelivered code remained active")
	}
	for _, key := range []string{harukiRedis.BuildQQMailSendRateLimitUserKey(owner), <-ipKeys, cache.KeyBuilder().BuildEmailVerifySendRateLimitTargetKey(email)} {
		if count, err := cache.Redis.Get(ctx, key).Int(); err != nil || count != 0 {
			t.Fatalf("definite SMTP failure left allowance charged, count %d, err %v", count, err)
		}
	}
}

func TestEmailSendRefundOnlyAffectsItsOriginalWindowsOnce(t *testing.T) {
	t.Parallel()
	helper, cache, ctx := newEmailVerifyTestHelper(t)
	reserved := make(chan []string, 1)
	app := fiber.New()
	app.Post("/", func(c fiber.Ctx) error {
		reservation, err := reserveEmailSend(c, helper, "owner", "123456@qq.com")
		if err == nil {
			reserved <- reservation
		}
		return err
	})
	reserve := func() []string {
		t.Helper()
		resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/", nil))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("reservation returned %d", resp.StatusCode)
		}
		return <-reserved
	}
	refund := func(keys []string) {
		t.Helper()
		if err := cache.Redis.Eval(ctx, emailSendRateLimitReleaseScript, keys).Err(); err != nil {
			t.Fatal(err)
		}
	}
	assertCounts := func(keys []string, want int) {
		t.Helper()
		for _, key := range keys {
			if count, err := cache.Redis.Get(ctx, key).Int(); err != nil || count != want {
				t.Fatalf("counter = %d, want %d, err %v", count, want, err)
			}
		}
	}
	first := reserve()
	// Model one expired bucket while its receipt and the other buckets live.
	if err := cache.Redis.Del(ctx, first[0]).Err(); err != nil {
		t.Fatal(err)
	}
	second := reserve()
	refund(first)
	assertCounts(second[:3], 1)

	third := reserve()
	refund(second)
	refund(second)
	assertCounts(third[:3], 1)
}

func TestEmailSendStopsBeforeWritingChallengeWhenQuotaStoreFails(t *testing.T) {
	t.Parallel()
	helper, cache, ctx := newEmailVerifyTestHelper(t)
	const owner, email = "owner", "123456@qq.com"
	helper.SMTPClient = &smtp.HarukiSMTPClient{}
	target := cache.KeyBuilder().BuildEmailVerifySendRateLimitTargetKey(email)
	if err := cache.Redis.Set(ctx, target, "invalid counter", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Post("/", func(c fiber.Ctx) error {
		return SendEmailHandler(c, owner, email, "valid", helper, captchaResult{
			response: &harukiCloudflare.TurnstileResponse{Success: true},
		})
	})
	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(raw), "verification service unavailable") {
		t.Fatalf("quota failure returned status %d, read error %v", resp.StatusCode, err)
	}
	challenge := cache.KeyBuilder().BuildOwnedEmailVerifyKey(owner, email)
	count, err := cache.Redis.Exists(ctx, challenge, harukiRedis.BuildQQMailSendRateLimitUserKey(owner)).Result()
	if err != nil || count != 0 {
		t.Fatalf("quota failure stored a challenge or charged another bucket, count %d, err %v", count, err)
	}
}

func TestEmailVerificationRestoresOnFailureWithoutOverwritingNewCode(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name   string
		rotate bool
		want   string
	}{
		{name: "binding unavailable", want: "first:123456"},
		{name: "code rotated during binding", rotate: true, want: "second:654321"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			helper, cache, ctx := newEmailVerifyTestHelper(t)
			const owner, email = "owner", "123456@qq.com"
			key := cache.KeyBuilder().BuildOwnedEmailVerifyKey(owner, email)
			if err := cache.Redis.Set(ctx, key, "first:123456", time.Minute).Err(); err != nil {
				t.Fatal(err)
			}
			app := fiber.New()
			app.Post("/", func(c fiber.Ctx) error {
				return VerifyEmailHandler(c, owner, email, "123456", helper, func() error {
					if scenario.rotate {
						if err := cache.Redis.Set(ctx, key, "second:654321", time.Minute).Err(); err != nil {
							return err
						}
					}
					return fiber.ErrServiceUnavailable
				})
			})
			resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("binding failure returned status %d", resp.StatusCode)
			}
			if got, err := cache.Redis.Get(ctx, key).Result(); err != nil || got != scenario.want {
				t.Fatalf("wrong challenge after failed binding, err %v", err)
			}
			if ttl, err := cache.Redis.PTTL(ctx, key).Result(); err != nil || ttl <= 0 || ttl > time.Minute {
				t.Fatalf("binding failure changed challenge lifetime to %v, err %v", ttl, err)
			}
			if !scenario.rotate && runVerifyEmailHandlerStatus(t, helper, owner, email, "123456") != http.StatusNoContent {
				t.Fatal("a retry could not use the restored challenge")
			}
		})
	}
}

func TestEmailVerificationReservesCodeUntilBindingCompletes(t *testing.T) {
	t.Parallel()
	helper, cache, ctx := newEmailVerifyTestHelper(t)
	const owner, email = "owner", "123456@qq.com"
	key := cache.KeyBuilder().BuildOwnedEmailVerifyKey(owner, email)
	if err := cache.Redis.Set(ctx, key, "first:123456", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	started, unblock := make(chan struct{}, 2), make(chan struct{})
	release := sync.OnceFunc(func() { close(unblock) })
	defer release()
	app := fiber.New()
	app.Post("/", func(c fiber.Ctx) error {
		return VerifyEmailHandler(c, owner, email, "123456", helper, func() error {
			started <- struct{}{}
			<-unblock
			return nil
		})
	})
	response := func() int {
		resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/", nil))
		if err != nil {
			return -1
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	first := make(chan int, 1)
	go func() { first <- response() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("binding did not start")
	}
	second := make(chan int, 1)
	go func() { second <- response() }()
	select {
	case status := <-second:
		if status != http.StatusBadRequest {
			t.Fatalf("a second verification returned %d while the code was reserved", status)
		}
	case <-started:
		t.Fatal("a second binding started with the reserved code")
	case <-time.After(time.Second):
		t.Fatal("second verification did not finish")
	}
	release()
	if status := <-first; status != http.StatusOK {
		t.Fatalf("binding returned status %d", status)
	}
	if count, err := cache.Redis.Exists(ctx, key).Result(); err != nil || count != 0 {
		t.Fatalf("successful binding left the challenge available, count %d, err %v", count, err)
	}
}

func TestVerificationFailsClosedWhenRateStoreIsUnavailable(t *testing.T) {
	t.Parallel()
	helper, cache, ctx := newEmailVerifyTestHelper(t)
	const owner, email = "owner", "123456@qq.com"
	key := cache.KeyBuilder().BuildOwnedEmailVerifyKey(owner, email)
	if err := cache.Redis.Set(ctx, key, "fixture:123456", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if err := cache.Redis.Set(ctx, cache.KeyBuilder().BuildOwnedEmailAttemptKey("fixture"), "invalid", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if status := runVerifyEmailHandlerStatus(t, helper, owner, email, "123456"); status != http.StatusServiceUnavailable {
		t.Fatalf("unavailable attempt store returned %d", status)
	}
	if got := cache.Redis.Exists(ctx, key).Val(); got != 1 {
		t.Fatal("a code was consumed without quota admission")
	}
}

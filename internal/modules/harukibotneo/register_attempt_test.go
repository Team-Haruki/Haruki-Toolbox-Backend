package harukibotneo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/neopg/enttest"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiSMTP "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/smtp"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
	goredis "github.com/redis/go-redis/v9"
)

func TestRecordInvalidVerificationAttemptUsesAtomicLimit(t *testing.T) {
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error: %v", err)
	}
	t.Cleanup(server.Close)

	client := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	manager := &harukiRedis.HarukiRedisManager{Redis: client}
	ctx := context.Background()
	const attemptKey = "bot:verify:attempt:test"
	const codeKey = "bot:verify:code:test"
	if err := manager.SetCache(ctx, codeKey, "123456", verifyCodeTTL); err != nil {
		t.Fatalf("seed verification code: %v", err)
	}

	for attempt := 1; attempt <= maxVerifyAttempts; attempt++ {
		limited, err := recordInvalidVerificationAttempt(ctx, manager, attemptKey, codeKey, "123456")
		if err != nil {
			t.Fatalf("attempt %d returned error: %v", attempt, err)
		}
		if limited {
			t.Fatalf("attempt %d was limited before maxVerifyAttempts", attempt)
		}
	}

	limited, err := recordInvalidVerificationAttempt(ctx, manager, attemptKey, codeKey, "123456")
	if err != nil {
		t.Fatalf("over-limit attempt returned error: %v", err)
	}
	if !limited {
		t.Fatal("over-limit attempt was not rejected")
	}
	if exists := server.Exists(codeKey); exists {
		t.Fatal("verification code remained after too many attempts")
	}
	if got, err := server.Get(attemptKey); err != nil || got != "6" {
		t.Fatalf("attempt counter after invalidation = %q, %v; want 6 retained until TTL", got, err)
	}
}

func TestRecordInvalidVerificationAttemptCannotResetUnderConcurrency(t *testing.T) {
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error: %v", err)
	}
	t.Cleanup(server.Close)

	client := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	manager := &harukiRedis.HarukiRedisManager{Redis: client}
	ctx := context.Background()
	const attemptKey = "bot:verify:attempt:concurrent"
	const codeKey = "bot:verify:code:concurrent"
	if err := manager.SetCache(ctx, codeKey, "123456", verifyCodeTTL); err != nil {
		t.Fatalf("seed verification code: %v", err)
	}

	const requests = 32
	start := make(chan struct{})
	var ready sync.WaitGroup
	var finished sync.WaitGroup
	var allowed atomic.Int64
	var limited atomic.Int64
	var failed atomic.Int64
	ready.Add(requests)
	finished.Add(requests)
	for range requests {
		go func() {
			defer finished.Done()
			ready.Done()
			<-start
			wasLimited, err := recordInvalidVerificationAttempt(ctx, manager, attemptKey, codeKey, "123456")
			switch {
			case err != nil:
				failed.Add(1)
			case wasLimited:
				limited.Add(1)
			default:
				allowed.Add(1)
			}
		}()
	}
	ready.Wait()
	close(start)
	finished.Wait()

	if failed.Load() != 0 {
		t.Fatalf("concurrent attempt errors = %d, want 0", failed.Load())
	}
	if got := allowed.Load(); got != int64(maxVerifyAttempts) {
		t.Fatalf("concurrent attempts below limit = %d, want exactly %d", got, maxVerifyAttempts)
	}
	if got := limited.Load(); got != requests-int64(maxVerifyAttempts) {
		t.Fatalf("concurrent limited attempts = %d, want %d", got, requests-int64(maxVerifyAttempts))
	}
	if got, err := server.Get(attemptKey); err != nil || got != "32" {
		t.Fatalf("final atomic counter = %q, %v; want 32", got, err)
	}
	if server.Exists(codeKey) {
		t.Fatal("verification code remained after concurrent limit was exceeded")
	}
}

func TestBotVerificationUsesIndependentActorAndRecipientBudgets(t *testing.T) {
	server := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	keys := harukiRedis.NewKeyBuilder("fixture")
	if keys.BuildBotVerifyCodeKey("first", "123") == keys.BuildBotVerifyCodeKey("other", "123") {
		t.Fatal("verification codes share state across actors")
	}
	if keys.BuildBotRegisterRateLimitUserKey("first") == keys.BuildBotRegisterRateLimitUserKey("other") {
		t.Fatal("pre-proof register limits share state across actors")
	}
	redisKeys := []string{"ip", "recipient"}
	if err := client.Set(t.Context(), redisKeys[0], sendMailIPLimit, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	limits, err := client.Eval(t.Context(), sendMailRateLimitScript, redisKeys, sendMailIPLimit, sendMailTargetLimit, 60000).Int64Slice()
	if err != nil || limits[0] != rateLimitLimitedByIP || server.Exists(redisKeys[1]) {
		t.Fatalf("rejected sender spent recipient budget: %v, %v", limits, err)
	}
}

func TestBotRegistrationUsesActorScopedVerification(t *testing.T) {
	server := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	botDB := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = botDB.Close() })
	manager := &harukiRedis.HarukiRedisManager{Redis: client}
	helper := &api.HarukiToolboxRouterHelpers{
		DBManager:              &database.HarukiToolboxDBManager{BotDB: botDB, Redis: manager},
		BotRegistrationEnabled: true,
		BotCredentialSignToken: "fixture-signing-token",
		SMTPClient:             &harukiSMTP.HarukiSMTPClient{Addr: "invalid-smtp-address", From: "test@example.test"},
	}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", c.Get("Fixture-Actor"))
		return c.Next()
	})
	app.Post("/register", handleRegister(helper))
	app.Post("/mail", handleSendMail(helper))
	post := func(path, actor, body string, want int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		if actor != "" {
			req.Header.Set("Fixture-Actor", actor)
		}
		resp, err := app.Test(req, fiber.TestConfig{Timeout: 10 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("%s as %q returned %d, want %d", path, actor, resp.StatusCode, want)
		}
	}
	const registration = `{"qq_number":123,"verification_code":"123456"}`
	const email = `{"qq_number":123}`
	post("/register", "", registration, http.StatusUnauthorized)
	post("/mail", "", email, http.StatusUnauthorized)
	key := manager.KeyBuilder().BuildBotVerifyCodeKey("owner", "123")
	if err := manager.SetCache(t.Context(), key, "owner-challenge.123456", verifyCodeTTL); err != nil {
		t.Fatal(err)
	}
	for range registerActorLimit {
		post("/register", "other-actor", registration, http.StatusBadRequest)
	}
	post("/register", "other-actor", registration, http.StatusTooManyRequests)
	post("/mail", "other-actor", email, http.StatusInternalServerError)
	otherCode := manager.KeyBuilder().BuildBotVerifyCodeKey("other-actor", "123")
	targetBudget := manager.KeyBuilder().BuildBotSendMailRateLimitTargetKey("123")
	if server.Exists(otherCode) || server.Exists(targetBudget) || !server.Exists(key) {
		t.Fatal("failed mail did not roll back its own challenge and budget")
	}
	post("/register", "owner", registration, http.StatusCreated)
	post("/register", "owner", registration, http.StatusBadRequest)
	if server.Exists(key) || botDB.User.Query().CountX(t.Context()) != 1 {
		t.Fatal("successful registration did not consume the user's verification challenge")
	}
}

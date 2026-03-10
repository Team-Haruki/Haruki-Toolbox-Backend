package usergamebindings

import (
	"context"
	"errors"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"haruki-suite/utils/database/postgresql"
	harukiRedis "haruki-suite/utils/database/redis"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	goredis "github.com/redis/go-redis/v9"
)

func TestIsNumericGameUserID(t *testing.T) {
	t.Parallel()

	if !isNumericGameUserID("1241241241") {
		t.Fatalf("numeric game user id should be valid")
	}
	if isNumericGameUserID("") {
		t.Fatalf("empty game user id should be invalid")
	}
	if isNumericGameUserID("abc123") {
		t.Fatalf("non-numeric game user id should be invalid")
	}
	if isNumericGameUserID("123 456") {
		t.Fatalf("spaced game user id should be invalid")
	}
}

func TestBindingOwnerHelpers(t *testing.T) {
	t.Parallel()

	ownerUser := &postgresql.User{ID: "u-1"}
	bindingOwned := &postgresql.GameAccountBinding{}
	bindingOwned.Edges.User = ownerUser

	bindingOrphan := &postgresql.GameAccountBinding{}

	if got := bindingOwnerID(nil); got != "" {
		t.Fatalf("bindingOwnerID(nil) = %q, want empty", got)
	}
	if got := bindingOwnerID(bindingOrphan); got != "" {
		t.Fatalf("bindingOwnerID(orphan) = %q, want empty", got)
	}
	if got := bindingOwnerID(bindingOwned); got != "u-1" {
		t.Fatalf("bindingOwnerID(owned) = %q, want %q", got, "u-1")
	}

	if !bindingOwnerMissing(bindingOrphan) {
		t.Fatalf("bindingOwnerMissing(orphan) should be true")
	}
	if bindingOwnerMissing(bindingOwned) {
		t.Fatalf("bindingOwnerMissing(owned) should be false")
	}

	if !isBindingOwnedByUser(bindingOwned, "u-1") {
		t.Fatalf("isBindingOwnedByUser should return true for same user")
	}
	if isBindingOwnedByUser(bindingOwned, "u-2") {
		t.Fatalf("isBindingOwnedByUser should return false for different user")
	}
	if isBindingOwnedByUser(bindingOrphan, "u-1") {
		t.Fatalf("isBindingOwnedByUser should return false for orphan binding")
	}
}

func TestCheckExistingBindingAllowsOrphanBinding(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		orphan := &postgresql.GameAccountBinding{}
		if err := checkExistingBinding(c, context.Background(), nil, orphan, "u-1"); err != nil {
			return err
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusNoContent {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
	}
}

func newGameBindingRedisHelper(t *testing.T) (*harukiAPIHelper.HarukiToolboxRouterHelpers, *harukiRedis.HarukiRedisManager, context.Context) {
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

func TestGetVerificationCodeTooManyAttempts(t *testing.T) {
	t.Parallel()

	helper, redisManager, ctx := newGameBindingRedisHelper(t)
	userID, server, gameUserID := "u1", "jp", "12345"
	attemptKey := harukiRedis.BuildGameAccountVerifyAttemptKey(userID, server, gameUserID)
	codeKey := harukiRedis.BuildGameAccountVerifyKey(userID, server, gameUserID)

	if err := redisManager.SetCache(ctx, attemptKey, gameAccountVerificationMaxAttempts, time.Minute); err != nil {
		t.Fatalf("seed attempt key error: %v", err)
	}
	if err := redisManager.SetCache(ctx, codeKey, "1/2/3/4/5/6", time.Minute); err != nil {
		t.Fatalf("seed code key error: %v", err)
	}

	_, err := getVerificationCode(ctx, helper, userID, server, gameUserID)
	if !errors.Is(err, errGameAccountVerificationTooManyAttempts) {
		t.Fatalf("error = %v, want %v", err, errGameAccountVerificationTooManyAttempts)
	}
}

func TestConsumeGameAccountVerificationCode(t *testing.T) {
	t.Parallel()

	helper, redisManager, ctx := newGameBindingRedisHelper(t)
	userID, server, gameUserID := "u1", "jp", "12345"
	code := "1/2/3/4/5/6"
	attemptKey := harukiRedis.BuildGameAccountVerifyAttemptKey(userID, server, gameUserID)
	codeKey := harukiRedis.BuildGameAccountVerifyKey(userID, server, gameUserID)

	if err := redisManager.SetCache(ctx, attemptKey, 2, time.Minute); err != nil {
		t.Fatalf("seed attempt key error: %v", err)
	}
	if err := redisManager.SetCache(ctx, codeKey, code, time.Minute); err != nil {
		t.Fatalf("seed code key error: %v", err)
	}

	if err := consumeGameAccountVerificationCode(ctx, helper, userID, server, gameUserID, code); err != nil {
		t.Fatalf("consumeGameAccountVerificationCode error: %v", err)
	}

	exists, err := redisManager.Redis.Exists(ctx, codeKey, attemptKey).Result()
	if err != nil {
		t.Fatalf("exists check error: %v", err)
	}
	if exists != 0 {
		t.Fatalf("expected verification and attempt keys to be removed, exists=%d", exists)
	}
}

func TestShouldIncrementGameAccountVerificationAttempt(t *testing.T) {
	t.Parallel()

	if !shouldIncrementGameAccountVerificationAttempt(errGameAccountVerificationCodeMissing) {
		t.Fatalf("missing-code error should increment attempts")
	}
	if !shouldIncrementGameAccountVerificationAttempt(errGameAccountVerificationCodeMismatch) {
		t.Fatalf("mismatch error should increment attempts")
	}
	if shouldIncrementGameAccountVerificationAttempt(errors.New("server unavailable")) {
		t.Fatalf("non-verification error should not increment attempts")
	}
}

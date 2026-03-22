package usergamebindings

import (
	"context"
	"errors"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database"
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

func TestClassifyExistingBinding(t *testing.T) {
	t.Parallel()

	orphan := &postgresql.GameAccountBinding{}
	if got := classifyExistingBinding(orphan, "u-1"); got != existingBindingStateNone {
		t.Fatalf("classify orphan = %v, want %v", got, existingBindingStateNone)
	}

	ownedByOther := &postgresql.GameAccountBinding{}
	ownedByOther.Edges.User = &postgresql.User{ID: "u-2"}
	if got := classifyExistingBinding(ownedByOther, "u-1"); got != existingBindingStateOwnedByOther {
		t.Fatalf("classify owned-by-other = %v, want %v", got, existingBindingStateOwnedByOther)
	}

	verifiedBySelf := &postgresql.GameAccountBinding{Verified: true}
	verifiedBySelf.Edges.User = &postgresql.User{ID: "u-1"}
	if got := classifyExistingBinding(verifiedBySelf, "u-1"); got != existingBindingStateVerifiedBySelf {
		t.Fatalf("classify verified-by-self = %v, want %v", got, existingBindingStateVerifiedBySelf)
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

func TestMapGameAccountVerificationCodeLookupError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		inputErr   error
		wantCode   int
		wantDetail string
	}{
		{
			name:       "too many attempts",
			inputErr:   errGameAccountVerificationTooManyAttempts,
			wantCode:   fiber.StatusBadRequest,
			wantDetail: "too many verification attempts, please generate a new code",
		},
		{
			name:       "code expired",
			inputErr:   errGameAccountVerificationCodeExpired,
			wantCode:   fiber.StatusBadRequest,
			wantDetail: "verification code expired or not found",
		},
		{
			name:       "storage unavailable",
			inputErr:   errGameAccountVerificationServiceUnstable,
			wantCode:   fiber.StatusInternalServerError,
			wantDetail: "verification service unavailable",
		},
		{
			name:       "unknown lookup error",
			inputErr:   errors.New("boom"),
			wantCode:   fiber.StatusBadRequest,
			wantDetail: "verification code not found",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mapGameAccountVerificationCodeLookupError(tc.inputErr)
			if got == nil {
				t.Fatalf("mapGameAccountVerificationCodeLookupError(%v) returned nil", tc.inputErr)
			}
			if got.Code != tc.wantCode {
				t.Fatalf("status code = %d, want %d", got.Code, tc.wantCode)
			}
			if got.Message != tc.wantDetail {
				t.Fatalf("message = %q, want %q", got.Message, tc.wantDetail)
			}
		})
	}
}

func TestMapGameAccountOwnershipVerificationError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		inputErr   error
		wantCode   int
		wantDetail string
	}{
		{
			name:       "missing code in profile",
			inputErr:   errGameAccountVerificationCodeMissing,
			wantCode:   fiber.StatusBadRequest,
			wantDetail: "verification code missing in game profile",
		},
		{
			name:       "code mismatch",
			inputErr:   errGameAccountVerificationCodeMismatch,
			wantCode:   fiber.StatusBadRequest,
			wantDetail: "verification code does not match game profile",
		},
		{
			name:       "account not found",
			inputErr:   errGameAccountNotFound,
			wantCode:   fiber.StatusBadRequest,
			wantDetail: "game account not found",
		},
		{
			name:       "server unavailable",
			inputErr:   errGameAccountServerUnavailable,
			wantCode:   fiber.StatusBadGateway,
			wantDetail: "game server unavailable",
		},
		{
			name:       "upstream request failed",
			inputErr:   errors.Join(errGameAccountProfileRequestFailed, errors.New("timeout")),
			wantCode:   fiber.StatusBadGateway,
			wantDetail: "failed to query game account profile",
		},
		{
			name:       "empty profile",
			inputErr:   errGameAccountProfileEmpty,
			wantCode:   fiber.StatusBadGateway,
			wantDetail: "empty game account profile response",
		},
		{
			name:       "invalid profile",
			inputErr:   errors.Join(errGameAccountProfileInvalid, errors.New("bad json")),
			wantCode:   fiber.StatusBadGateway,
			wantDetail: "invalid game account profile response",
		},
		{
			name:       "unexpected error",
			inputErr:   errors.New("panic"),
			wantCode:   fiber.StatusInternalServerError,
			wantDetail: "failed to verify game account ownership",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mapGameAccountOwnershipVerificationError(tc.inputErr)
			if got == nil {
				t.Fatalf("mapGameAccountOwnershipVerificationError(%v) returned nil", tc.inputErr)
			}
			if got.Code != tc.wantCode {
				t.Fatalf("status code = %d, want %d", got.Code, tc.wantCode)
			}
			if got.Message != tc.wantDetail {
				t.Fatalf("message = %q, want %q", got.Message, tc.wantDetail)
			}
		})
	}
}

package usergamebindings

import (
	"context"
	"errors"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
)

const (
	gameAccountVerificationTTL         = 5 * time.Minute
	gameAccountVerificationTargetLimit = 30
	gameAccountVerificationMaxAttempts = 5
	gameAccountProfileChecksPerUser    = 12
	gameAccountProfileChecksPerTarget  = 30
)

var (
	errGameAccountVerificationCodeMissing     = errors.New("verification code missing in user profile")
	errGameAccountVerificationCodeMismatch    = errors.New("verification code mismatch")
	errGameAccountVerificationCodeExpired     = errors.New("verification code expired or not found")
	errGameAccountVerificationTooManyAttempts = errors.New("too many verification attempts, please generate a new code")
	errGameAccountProfileChecksLimited        = errors.New("too many game profile checks, please try again later")
	errGameAccountVerificationServiceUnstable = errors.New("verification service unavailable")
)

func getVerificationCode(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID, serverStr, gameUserIDStr string) (string, error) {
	storageKey := harukiRedis.BuildGameAccountVerifyKey(userID, serverStr, gameUserIDStr)
	var code string
	ok, err := apiHelper.DBManager.Redis.GetCache(ctx, storageKey, &code)
	if err != nil {
		return "", errGameAccountVerificationServiceUnstable
	}
	if !ok {
		return "", errGameAccountVerificationCodeExpired
	}
	if err := reserveGameAccountVerificationAttempt(ctx, apiHelper, userID, serverStr, gameUserIDStr, code); err != nil {
		return "", err
	}
	return code, nil
}

// reserveGameAccountVerificationAttempt atomically claims one of the bounded
// upstream profile checks for this issuance. A replacement code has its own
// budget, even when another request still holds the previous code.
func reserveGameAccountVerificationAttempt(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID, serverStr, gameUserIDStr, code string) error {
	redis := apiHelper.DBManager.Redis
	attemptKey := redis.KeyBuilder().BuildGameAccountVerifyAttemptKey(userID, serverStr, gameUserIDStr, code)
	count, err := redis.IncrementWithTTL(ctx, attemptKey, gameAccountVerificationTTL)
	if err != nil {
		return errGameAccountVerificationServiceUnstable
	}
	if count > gameAccountVerificationMaxAttempts {
		return errGameAccountVerificationTooManyAttempts
	}
	// Charge the expensive upstream request to budgets that survive code
	// replacement and successful consumption.
	for _, budget := range []struct {
		key string
		max int64
	}{
		{redis.KeyBuilder().BuildGameAccountProfileCheckUserKey(userID), gameAccountProfileChecksPerUser},
		{harukiRedis.BuildGameAccountProfileCheckTargetKey(serverStr, gameUserIDStr), gameAccountProfileChecksPerTarget},
	} {
		count, err := redis.IncrementWithTTL(ctx, budget.key, gameAccountVerificationTTL)
		if err != nil {
			return errGameAccountVerificationServiceUnstable
		}
		if count > budget.max {
			return errGameAccountProfileChecksLimited
		}
	}
	return nil
}

func consumeGameAccountVerificationCode(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID, serverStr, gameUserIDStr, expectedCode string) error {
	storageKey := harukiRedis.BuildGameAccountVerifyKey(userID, serverStr, gameUserIDStr)
	consumed, err := apiHelper.DBManager.Redis.DeleteCacheIfValueMatches(ctx, storageKey, expectedCode)
	if err != nil {
		return errGameAccountVerificationServiceUnstable
	}
	if !consumed {
		return errGameAccountVerificationCodeExpired
	}

	attemptKey := apiHelper.DBManager.Redis.KeyBuilder().BuildGameAccountVerifyAttemptKey(userID, serverStr, gameUserIDStr, expectedCode)
	if err := apiHelper.DBManager.Redis.DeleteCache(ctx, attemptKey); err != nil {
		harukiLogger.Warnf("Failed to clear game account verification attempt key: %v", err)
	}
	return nil
}

func mapGameAccountVerificationCodeLookupError(err error) *fiber.Error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, errGameAccountVerificationTooManyAttempts):
		return fiber.NewError(fiber.StatusBadRequest, "too many verification attempts, please generate a new code")
	case errors.Is(err, errGameAccountProfileChecksLimited):
		return fiber.NewError(fiber.StatusTooManyRequests, err.Error())
	case errors.Is(err, errGameAccountVerificationCodeExpired):
		return fiber.NewError(fiber.StatusBadRequest, "verification code expired or not found")
	case errors.Is(err, errGameAccountVerificationServiceUnstable):
		return fiber.NewError(fiber.StatusInternalServerError, "verification service unavailable")
	default:
		return fiber.NewError(fiber.StatusBadRequest, "verification code not found")
	}
}

func mapGameAccountOwnershipVerificationError(err error) *fiber.Error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, errGameAccountVerificationCodeMissing):
		return fiber.NewError(fiber.StatusBadRequest, "verification code missing in game profile")
	case errors.Is(err, errGameAccountVerificationCodeMismatch):
		return fiber.NewError(fiber.StatusBadRequest, "verification code does not match game profile")
	case errors.Is(err, errGameAccountNotFound):
		return fiber.NewError(fiber.StatusBadRequest, "game account not found")
	case errors.Is(err, errGameAccountServerUnavailable):
		return fiber.NewError(fiber.StatusBadGateway, "game server unavailable")
	case errors.Is(err, errGameAccountProfileRequestFailed):
		return fiber.NewError(fiber.StatusBadGateway, "failed to query game account profile")
	case errors.Is(err, errGameAccountProfileEmpty):
		return fiber.NewError(fiber.StatusBadGateway, "empty game account profile response")
	case errors.Is(err, errGameAccountProfileInvalid):
		return fiber.NewError(fiber.StatusBadGateway, "invalid game account profile response")
	default:
		return fiber.NewError(fiber.StatusInternalServerError, "failed to verify game account ownership")
	}
}

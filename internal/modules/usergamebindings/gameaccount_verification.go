package usergamebindings

import (
	"context"
	"errors"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
)

const (
	gameAccountVerificationTTL         = 5 * time.Minute
	gameAccountVerificationMaxAttempts = 5
)

var (
	errGameAccountVerificationCodeMissing     = errors.New("verification code missing in user profile")
	errGameAccountVerificationCodeMismatch    = errors.New("verification code mismatch")
	errGameAccountVerificationCodeExpired     = errors.New("verification code expired or not found")
	errGameAccountVerificationTooManyAttempts = errors.New("too many verification attempts, please generate a new code")
	errGameAccountVerificationServiceUnstable = errors.New("verification service unavailable")
)

func getVerificationCode(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID, serverStr, gameUserIDStr string) (string, error) {
	attemptKey := harukiRedis.BuildGameAccountVerifyAttemptKey(userID, serverStr, gameUserIDStr)
	var attemptCount int
	found, err := apiHelper.DBManager.Redis.GetCache(ctx, attemptKey, &attemptCount)
	if err != nil {
		return "", errGameAccountVerificationServiceUnstable
	}
	if found && attemptCount >= gameAccountVerificationMaxAttempts {
		return "", errGameAccountVerificationTooManyAttempts
	}

	storageKey := harukiRedis.BuildGameAccountVerifyKey(userID, serverStr, gameUserIDStr)
	var code string
	ok, err := apiHelper.DBManager.Redis.GetCache(ctx, storageKey, &code)
	if err != nil {
		return "", errGameAccountVerificationServiceUnstable
	}
	if !ok {
		return "", errGameAccountVerificationCodeExpired
	}
	return code, nil
}

func incrementGameAccountVerificationAttempt(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID, serverStr, gameUserIDStr string) error {
	attemptKey := harukiRedis.BuildGameAccountVerifyAttemptKey(userID, serverStr, gameUserIDStr)
	_, err := apiHelper.DBManager.Redis.IncrementWithTTL(ctx, attemptKey, gameAccountVerificationTTL)
	return err
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

	attemptKey := harukiRedis.BuildGameAccountVerifyAttemptKey(userID, serverStr, gameUserIDStr)
	if err := apiHelper.DBManager.Redis.DeleteCache(ctx, attemptKey); err != nil {
		harukiLogger.Warnf("Failed to clear game account verification attempt key: %v", err)
	}
	return nil
}

func shouldIncrementGameAccountVerificationAttempt(err error) bool {
	return errors.Is(err, errGameAccountVerificationCodeMissing) || errors.Is(err, errGameAccountVerificationCodeMismatch)
}

func mapGameAccountVerificationCodeLookupError(err error) *fiber.Error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, errGameAccountVerificationTooManyAttempts):
		return fiber.NewError(fiber.StatusBadRequest, "too many verification attempts, please generate a new code")
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

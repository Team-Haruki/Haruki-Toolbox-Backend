package usersocial

import (
	"crypto/subtle"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/socialplatforminfo"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
)

func handleVerifySocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		targetUserID := ""
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.social_platform.verify_bot", result, targetUserID, map[string]any{
				"reason": reason,
			})
		}()

		token, ok := extractBearerToken(c.Get("Authorization"))
		if !ok {
			reason = "missing_authorization"
			return harukiAPIHelper.ErrorUnauthorized(c, "invalid authorization")
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(config.Cfg.UserSystem.SocialPlatformVerifyToken)) != 1 {
			reason = "invalid_authorization"
			return harukiAPIHelper.ErrorUnauthorized(c, "invalid authorization")
		}

		var req harukiAPIHelper.HarukiBotVerifySocialPlatformPayload
		if err := c.Bind().Body(&req); err != nil {
			reason = "invalid_payload"
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		if !isSupportedSocialPlatform(req.Platform) {
			reason = "unsupported_platform"
			return harukiAPIHelper.ErrorBadRequest(c, "unsupported platform")
		}
		attemptCount, err := getSocialPlatformVerifyAttemptCount(c, apiHelper, req.Platform, req.UserID)
		if err != nil {
			harukiLogger.Errorf("Failed to get social verify attempt count: %v", err)
			reason = "get_attempt_count_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to get verification key")
		}
		if attemptCount >= socialPlatformVerifyMaxAttempts {
			reason = "too_many_attempts"
			return harukiAPIHelper.ErrorBadRequest(c, "too many verification attempts, please generate a new code")
		}

		storageKey := harukiRedis.BuildSocialPlatformVerifyKey(string(req.Platform), req.UserID)
		var code string
		found, err := apiHelper.DBManager.Redis.GetCache(ctx, storageKey, &code)
		if err != nil {
			harukiLogger.Errorf("Failed to get redis cache: %v", err)
			reason = "get_verification_key_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to get verification key")
		}
		if !found {
			reason = "verification_key_not_found"
			return harukiAPIHelper.ErrorBadRequest(c, "verification key expired or not found")
		}
		if subtle.ConstantTimeCompare([]byte(req.OneTimePassword), []byte(code)) != 1 {
			if err := incrementSocialPlatformVerifyAttempt(c, apiHelper, req.Platform, req.UserID); err != nil {
				harukiLogger.Errorf("Failed to increment social verify attempt count: %v", err)
				reason = "increment_attempt_failed"
				return harukiAPIHelper.ErrorInternal(c, "failed to save verification state")
			}
			reason = "invalid_one_time_password"
			return harukiAPIHelper.ErrorUnauthorized(c, "invalid one time password")
		}

		userIDKey := harukiRedis.BuildSocialPlatformUserIDKey(string(req.Platform), req.UserID)
		var userID string
		found, err = apiHelper.DBManager.Redis.GetCache(ctx, userIDKey, &userID)
		if err != nil {
			harukiLogger.Errorf("Failed to get redis cache: %v", err)
			reason = "get_user_mapping_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to get userID")
		}
		if !found {
			reason = "user_mapping_not_found"
			return harukiAPIHelper.ErrorBadRequest(c, "userID mapping expired or not found")
		}
		targetUserID = userID
		statusTokenMappingKey := harukiRedis.BuildSocialPlatformStatusTokenKey(string(req.Platform), req.UserID)
		var statusToken string
		found, err = apiHelper.DBManager.Redis.GetCache(ctx, statusTokenMappingKey, &statusToken)
		if err != nil {
			harukiLogger.Errorf("Failed to get redis cache: %v", err)
			reason = "get_status_token_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to get status token")
		}
		if !found {
			reason = "status_token_not_found"
			return harukiAPIHelper.ErrorBadRequest(c, "status token mapping expired or not found")
		}

		exists, err := apiHelper.DBManager.DB.SocialPlatformInfo.Query().
			Where(socialplatforminfo.PlatformEQ(string(req.Platform)),
				socialplatforminfo.PlatformUserID(req.UserID)).
			Exist(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to query social platform info: %v", err)
			reason = "query_social_platform_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to query database")
		}
		if exists {
			reason = "social_platform_conflict"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "this social platform account is already bound", nil)
		}

		tx, err := apiHelper.DBManager.DB.Tx(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to start social platform verify transaction: %v", err)
			reason = "start_transaction_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to update social platform info")
		}

		if _, err := tx.SocialPlatformInfo.
			Create().
			SetPlatform(string(req.Platform)).
			SetPlatformUserID(req.UserID).
			SetVerified(true).
			SetUserID(userID).
			Save(ctx); err != nil {
			_ = tx.Rollback()
			if postgresql.IsConstraintError(err) {
				reason = "social_platform_conflict"
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "this social platform account is already bound", nil)
			}
			harukiLogger.Errorf("Failed to create social platform info: %v", err)
			reason = "create_social_platform_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to update social platform info")
		}

		statusTokenKey := harukiRedis.BuildStatusTokenKey(statusToken)
		if err := apiHelper.DBManager.Redis.SetCache(ctx, statusTokenKey, "true", socialPlatformVerifyTTL); err != nil {
			_ = tx.Rollback()
			harukiLogger.Errorf("Failed to set redis cache: %v", err)
			reason = "set_status_token_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to save status token")
		}

		if err := tx.Commit(); err != nil {
			if resetErr := apiHelper.DBManager.Redis.SetCache(ctx, statusTokenKey, "false", socialPlatformVerifyTTL); resetErr != nil {
				harukiLogger.Warnf("Failed to restore social platform status token after commit failure: %v", resetErr)
			}
			harukiLogger.Errorf("Failed to commit social platform verify transaction: %v", err)
			reason = "commit_transaction_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to update social platform info")
		}

		if err := apiHelper.DBManager.Redis.DeleteCache(ctx, storageKey); err != nil {
			harukiLogger.Warnf("Failed to clear social platform verification key: %v", err)
		}
		clearSocialPlatformVerifyAttempt(c, apiHelper, req.Platform, req.UserID)
		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.SuccessResponse[string](c, "social platform verified", nil)
	}
}

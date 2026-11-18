package usersocial

import (
	"crypto/subtle"

	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsoncodec"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/socialplatforminfo"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
)

func handleVerifySocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, botVerifyConfig BotVerifyConfig) fiber.Handler {
	return func(c fiber.Ctx) error {
		token, ok := extractBearerToken(c.Get("Authorization"))
		if !ok || !botVerifyConfig.authorizes(token) {
			return harukiAPIHelper.ErrorUnauthorized(c, "invalid authorization")
		}
		ctx := c.Context()
		targetUserID := ""
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.social_platform.verify_bot", result, targetUserID, map[string]any{
				"reason": reason,
			})
		}()

		var req harukiAPIHelper.HarukiBotVerifySocialPlatformPayload
		if err := c.Bind().Body(&req); err != nil {
			reason = "invalid_payload"
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		if !isSupportedSocialPlatform(req.Platform) {
			reason = "unsupported_platform"
			return harukiAPIHelper.ErrorBadRequest(c, "unsupported platform")
		}
		if !validSocialVerificationUserID(req.UserID) {
			reason = "invalid_platform_user_id"
			return harukiAPIHelper.ErrorBadRequest(c, "invalid platform user ID")
		}
		redis := apiHelper.DBManager.Redis
		keys := redis.KeyBuilder()
		storageKey := keys.BuildSocialVerificationChallengeKey(string(req.Platform), req.UserID)
		var encodedChallenge string
		found, err := redis.GetCache(ctx, storageKey, &encodedChallenge)
		if err != nil {
			harukiLogger.Errorf("Failed to get redis cache: %v", err)
			reason = "get_verification_key_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to get verification key")
		}
		if !found {
			reason = "verification_key_not_found"
			return harukiAPIHelper.ErrorBadRequest(c, "verification key expired or not found")
		}
		var challenge socialVerificationChallenge
		if err := jsoncodec.Unmarshal([]byte(encodedChallenge), &challenge); err != nil || challenge.OwnerID == "" || challenge.StatusToken == "" {
			reason = "invalid_verification_challenge"
			return harukiAPIHelper.ErrorInternal(c, "failed to get verification key")
		}
		attemptKey := keys.BuildSocialVerificationChallengeAttemptKey(challenge.StatusToken)
		count, err := redis.IncrementWithTTL(ctx, attemptKey, socialPlatformVerifyTTL)
		if err != nil {
			harukiLogger.Errorf("Failed to reserve social verify attempt: %v", err)
			reason = "reserve_attempt_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to save verification state")
		}
		if count > socialPlatformVerifyMaxAttempts {
			reason = "too_many_attempts"
			return harukiAPIHelper.ErrorBadRequest(c, "too many verification attempts, please generate a new code")
		}
		digest := keys.SocialVerificationCodeDigest(challenge.StatusToken, req.OneTimePassword)
		if subtle.ConstantTimeCompare([]byte(digest), []byte(challenge.CodeDigest)) != 1 {
			reason = "invalid_one_time_password"
			return harukiAPIHelper.ErrorUnauthorized(c, "invalid one time password")
		}

		consumed, err := redis.DeleteCacheIfValueMatches(ctx, storageKey, encodedChallenge)
		if err != nil {
			reason = "consume_verification_challenge_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to save verification state")
		}
		if !consumed {
			reason = "verification_key_not_found"
			return harukiAPIHelper.ErrorBadRequest(c, "verification key expired or not found")
		}
		targetUserID = challenge.OwnerID

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
			return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusConflict, "this social platform account is already bound", nil)
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
			SetUserID(challenge.OwnerID).
			Save(ctx); err != nil {
			_ = tx.Rollback()
			if postgresql.IsConstraintError(err) {
				reason = "social_platform_conflict"
				return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusConflict, "this social platform account is already bound", nil)
			}
			harukiLogger.Errorf("Failed to create social platform info: %v", err)
			reason = "create_social_platform_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to update social platform info")
		}

		statusTokenKey := harukiRedis.BuildStatusTokenKey(challenge.StatusToken)
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

		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.Responses.SuccessResponse[string](c, "social platform verified", nil)
	}
}

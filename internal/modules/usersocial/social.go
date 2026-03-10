package usersocial

import (
	"crypto/subtle"
	"fmt"
	"haruki-suite/config"
	userCoreModule "haruki-suite/internal/modules/usercore"
	userEmailModule "haruki-suite/internal/modules/useremail"
	platformAuthHeader "haruki-suite/internal/platform/authheader"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/socialplatforminfo"
	userSchema "haruki-suite/utils/database/postgresql/user"
	harukiRedis "haruki-suite/utils/database/redis"
	harukiLogger "haruki-suite/utils/logger"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"strings"
)

type socialStatusTokenBinding struct {
	Platform string `json:"platform"`
	UserID   string `json:"userId"`
}

func handleSendQQMail(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		var req harukiAPIHelper.SendQQMailPayload
		if err := c.Bind().Body(&req); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		exists, err := apiHelper.DBManager.DB.SocialPlatformInfo.Query().
			Where(socialplatforminfo.PlatformEQ(
				string(harukiAPIHelper.SocialPlatformQQ)),
				socialplatforminfo.PlatformUserID(req.QQ)).
			Exist(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to query social platform info: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to query database")
		}
		if exists {
			return harukiAPIHelper.ErrorBadRequest(c, "QQ binding already exists")
		}
		email := fmt.Sprintf("%s@qq.com", req.QQ)
		return userEmailModule.SendEmailHandler(c, email, req.ChallengeToken, apiHelper)
	}
}

func handleVerifyQQMail(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.social_platform.qq.verify", result, userID, map[string]any{
				"reason": reason,
			})
		}()

		var req harukiAPIHelper.VerifyQQMailPayload
		if err := c.Bind().Body(&req); err != nil {
			reason = "invalid_payload"
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		email := fmt.Sprintf("%s@qq.com", req.QQ)
		ok, err := userEmailModule.VerifyEmailHandler(c, email, req.OneTimePassword, apiHelper)
		if err != nil {
			reason = "verify_email_otp_failed"
			return err
		}
		if !ok {
			reason = "verify_email_otp_failed"
			return harukiAPIHelper.ErrorBadRequest(c, "verification failed")
		}

		exists, err := apiHelper.DBManager.DB.SocialPlatformInfo.Query().
			Where(socialplatforminfo.PlatformEQ(
				string(harukiAPIHelper.SocialPlatformQQ)),
				socialplatforminfo.PlatformUserID(req.QQ)).
			Exist(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to query social platform info: %v", err)
			reason = "query_social_platform_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to query database")
		}
		if exists {
			reason = "social_platform_conflict"
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "QQ binding already exists", nil)
		}
		if _, err := apiHelper.DBManager.DB.SocialPlatformInfo.
			Create().
			SetPlatform("qq").
			SetPlatformUserID(req.QQ).
			SetVerified(true).
			SetUserID(userID).
			Save(ctx); err != nil {
			if postgresql.IsConstraintError(err) {
				reason = "social_platform_conflict"
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "QQ binding already exists", nil)
			}
			harukiLogger.Errorf("Failed to create social platform info: %v", err)
			reason = "create_social_platform_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to update social platform info")
		}

		ud := harukiAPIHelper.HarukiToolboxUserData{
			SocialPlatformInfo: &harukiAPIHelper.SocialPlatformInfo{
				Platform: string(harukiAPIHelper.SocialPlatformQQ),
				UserID:   req.QQ,
				Verified: true,
			},
		}
		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.SuccessResponse(c, "social platform verified", &ud)
	}
}

func handleGenerateVerificationCode(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.social_platform.code.generate", result, userID, map[string]any{
				"reason": reason,
			})
		}()

		var req harukiAPIHelper.GenerateSocialPlatformCodePayload
		if err := c.Bind().Body(&req); err != nil {
			reason = "invalid_payload"
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
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
			return harukiAPIHelper.ErrorBadRequest(c, "binding already exists")
		}

		if !isSupportedSocialPlatform(req.Platform) {
			reason = "unsupported_platform"
			return harukiAPIHelper.ErrorBadRequest(c, "unsupported platform")
		}
		code, err := userEmailModule.GenerateCode(false)
		if err != nil {
			harukiLogger.Errorf("Failed to generate code: %v", err)
			reason = "generate_code_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to generate verification code")
		}
		storageKey := harukiRedis.BuildSocialPlatformVerifyKey(string(req.Platform), req.UserID)
		statusToken := uuid.NewString()
		statusTokenKey := harukiRedis.BuildStatusTokenKey(statusToken)
		statusTokenOwnerKey := harukiRedis.BuildStatusTokenOwnerKey(statusToken)
		statusTokenBindingKey := harukiRedis.BuildStatusTokenBindingKey(statusToken)
		userIDKey := harukiRedis.BuildSocialPlatformUserIDKey(string(req.Platform), req.UserID)
		statusTokenMappingKey := harukiRedis.BuildSocialPlatformStatusTokenKey(string(req.Platform), req.UserID)
		if err := apiHelper.DBManager.Redis.SetCachesAtomically(ctx, []harukiRedis.CacheItem{
			{Key: storageKey, Value: code},
			{Key: statusTokenKey, Value: "false"},
			{Key: statusTokenOwnerKey, Value: userID},
			{Key: statusTokenBindingKey, Value: socialStatusTokenBinding{Platform: string(req.Platform), UserID: req.UserID}},
			{Key: userIDKey, Value: userID},
			{Key: statusTokenMappingKey, Value: statusToken},
		}, 5*time.Minute); err != nil {
			harukiLogger.Errorf("Failed to save social verification state: %v", err)
			reason = "save_verification_state_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to save verification state")
		}

		resp := harukiAPIHelper.GenerateSocialPlatformCodeResponse{
			Status:          fiber.StatusOK,
			Message:         "ok",
			StatusToken:     statusToken,
			OneTimePassword: code,
		}
		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.ResponseWithStruct(c, fiber.StatusOK, resp)
	}
}

func handleVerificationStatus(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		statusToken := c.Params("status_token")
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		statusTokenOwnerKey := harukiRedis.BuildStatusTokenOwnerKey(statusToken)
		var ownerUserID string
		found, err := apiHelper.DBManager.Redis.GetCache(ctx, statusTokenOwnerKey, &ownerUserID)
		if err != nil {
			harukiLogger.Errorf("Failed to get status token owner cache: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to get status")
		}
		if !found || !statusTokenOwnedByUser(ownerUserID, userID) {
			return harukiAPIHelper.ErrorBadRequest(c, "status token expired or not found")
		}
		statusTokenKey := harukiRedis.BuildStatusTokenKey(statusToken)
		statusTokenBindingKey := harukiRedis.BuildStatusTokenBindingKey(statusToken)
		var binding socialStatusTokenBinding
		found, err = apiHelper.DBManager.Redis.GetCache(ctx, statusTokenBindingKey, &binding)
		if err != nil {
			harukiLogger.Errorf("Failed to get status token binding cache: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to get status")
		}
		if !found || strings.TrimSpace(binding.Platform) == "" || strings.TrimSpace(binding.UserID) == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "status token expired or not found")
		}
		var status string
		found, err = apiHelper.DBManager.Redis.GetCache(ctx, statusTokenKey, &status)
		if err != nil {
			harukiLogger.Errorf("Failed to get redis cache: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to get status")
		}
		if !found {
			return harukiAPIHelper.ErrorBadRequest(c, "status token expired or not found")
		}
		if status == "false" {
			return harukiAPIHelper.ErrorBadRequest(c, "You have not verified yet")
		}
		if status == "true" {
			info, err := apiHelper.DBManager.DB.SocialPlatformInfo.Query().Where(
				socialplatforminfo.HasUserWith(userSchema.IDEQ(userID)),
				socialplatforminfo.PlatformEQ(binding.Platform),
				socialplatforminfo.PlatformUserIDEQ(binding.UserID),
			).Only(ctx)
			if err != nil {
				if postgresql.IsNotFound(err) {
					return harukiAPIHelper.ErrorNotFound(c, "verified social platform binding not found")
				}
				harukiLogger.Errorf("Failed to query social platform info: %v", err)
				return harukiAPIHelper.ErrorInternal(c, "failed to get social platform info")
			}
			ud := harukiAPIHelper.HarukiToolboxUserData{
				SocialPlatformInfo: &harukiAPIHelper.SocialPlatformInfo{
					Platform: info.Platform,
					UserID:   info.PlatformUserID,
					Verified: info.Verified,
				},
			}
			return harukiAPIHelper.SuccessResponse(c, "verification completed", &ud)
		}
		return harukiAPIHelper.ErrorInternal(c, "get status failed")
	}
}

func statusTokenOwnedByUser(ownerUserID, currentUserID string) bool {
	ownerUserID = strings.TrimSpace(ownerUserID)
	currentUserID = strings.TrimSpace(currentUserID)
	return ownerUserID != "" && currentUserID != "" && ownerUserID == currentUserID
}

func handleClearSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.social_platform.clear", result, userID, map[string]any{
				"reason": reason,
			})
		}()

		exists, err := apiHelper.DBManager.DB.SocialPlatformInfo.
			Query().
			Where(socialplatforminfo.HasUserWith(userSchema.IDEQ(userID))).
			Exist(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to query social platform info: %v", err)
			reason = "query_social_platform_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to query social platform info")
		}
		if !exists {
			reason = "social_platform_not_found"
			return harukiAPIHelper.ErrorBadRequest(c, "no social platform info found")
		}

		_, err = apiHelper.DBManager.DB.SocialPlatformInfo.
			Delete().
			Where(socialplatforminfo.HasUserWith(userSchema.IDEQ(userID))).
			Exec(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to delete social platform info: %v", err)
			reason = "clear_social_platform_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to clear social platform info")
		}

		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.SuccessResponse[string](c, "social platform info cleared successfully", nil)
	}
}

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
		if req.OneTimePassword != code {
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
		if err := apiHelper.DBManager.Redis.SetCache(ctx, statusTokenKey, "true", 5*time.Minute); err != nil {
			_ = tx.Rollback()
			harukiLogger.Errorf("Failed to set redis cache: %v", err)
			reason = "set_status_token_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to save status token")
		}

		if err := tx.Commit(); err != nil {
			if resetErr := apiHelper.DBManager.Redis.SetCache(ctx, statusTokenKey, "false", 5*time.Minute); resetErr != nil {
				harukiLogger.Warnf("Failed to restore social platform status token after commit failure: %v", resetErr)
			}
			harukiLogger.Errorf("Failed to commit social platform verify transaction: %v", err)
			reason = "commit_transaction_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to update social platform info")
		}

		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.SuccessResponse[string](c, "social platform verified", nil)
	}
}

func RegisterUserSocialRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	social := apiHelper.Router.Group("/api/user/:toolbox_user_id/social-platform")

	social.Post("/send-qq-mail", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleSendQQMail(apiHelper))
	social.Post("/verify-qq-mail", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleVerifyQQMail(apiHelper))
	social.Post("/generate-verification-code", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleGenerateVerificationCode(apiHelper))
	social.Get("/verification-status/:status_token", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleVerificationStatus(apiHelper))
	social.Delete("/clear", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleClearSocialPlatform(apiHelper))

	apiHelper.Router.Post("/api/verify-social-platform", handleVerifySocialPlatform(apiHelper))
}

func isSupportedSocialPlatform(platform harukiAPIHelper.SocialPlatform) bool {
	switch platform {
	case harukiAPIHelper.SocialPlatformQQ,
		harukiAPIHelper.SocialPlatformQQBot,
		harukiAPIHelper.SocialPlatformDiscord,
		harukiAPIHelper.SocialPlatformTelegram:
		return true
	default:
		return false
	}
}

func extractBearerToken(authHeader string) (string, bool) {
	return platformAuthHeader.ExtractBearerToken(authHeader)
}

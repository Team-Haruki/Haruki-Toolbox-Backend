package user

import (
	"crypto/subtle"
	"fmt"
	"haruki-suite/config"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql/socialplatforminfo"
	"haruki-suite/utils/database/postgresql/user"
	harukiRedis "haruki-suite/utils/database/redis"
	harukiLogger "haruki-suite/utils/logger"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

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
		return SendEmailHandler(c, email, req.ChallengeToken, apiHelper)
	}
}

func handleVerifyQQMail(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID := c.Locals("userID").(string)
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			writeUserAuditLog(c, apiHelper, "user.social_platform.qq.verify", result, userID, map[string]any{
				"reason": reason,
			})
		}()

		var req harukiAPIHelper.VerifyQQMailPayload
		if err := c.Bind().Body(&req); err != nil {
			reason = "invalid_payload"
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		email := fmt.Sprintf("%s@qq.com", req.QQ)
		ok, err := VerifyEmailHandler(c, email, req.OneTimePassword, apiHelper)
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
			return harukiAPIHelper.ErrorBadRequest(c, "QQ binding already exists")
		}
		if _, err := apiHelper.DBManager.DB.SocialPlatformInfo.
			Create().
			SetPlatform("qq").
			SetPlatformUserID(req.QQ).
			SetVerified(true).
			SetUserID(userID).
			Save(ctx); err != nil {
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
		userID := c.Locals("userID").(string)
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			writeUserAuditLog(c, apiHelper, "user.social_platform.code.generate", result, userID, map[string]any{
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

		switch req.Platform {
		case harukiAPIHelper.SocialPlatformQQ, harukiAPIHelper.SocialPlatformQQBot,
			harukiAPIHelper.SocialPlatformDiscord, harukiAPIHelper.SocialPlatformTelegram:

		default:
			reason = "unsupported_platform"
			return harukiAPIHelper.ErrorBadRequest(c, "unsupported platform")
		}
		code, err := GenerateCode(false)
		if err != nil {
			harukiLogger.Errorf("Failed to generate code: %v", err)
			reason = "generate_code_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to generate verification code")
		}
		storageKey := harukiRedis.BuildSocialPlatformVerifyKey(string(req.Platform), req.UserID)
		statusToken := uuid.NewString()
		if err := apiHelper.DBManager.Redis.SetCache(ctx, storageKey, code, 5*time.Minute); err != nil {
			harukiLogger.Errorf("Failed to set redis cache: %v", err)
			reason = "save_code_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to save code")
		}
		statusTokenKey := harukiRedis.BuildStatusTokenKey(statusToken)
		if err := apiHelper.DBManager.Redis.SetCache(ctx, statusTokenKey, "false", 5*time.Minute); err != nil {
			harukiLogger.Errorf("Failed to set redis cache: %v", err)
			reason = "save_status_token_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to save status token")
		}
		userIDKey := harukiRedis.BuildSocialPlatformUserIDKey(string(req.Platform), req.UserID)
		if err := apiHelper.DBManager.Redis.SetCache(ctx, userIDKey, userID, 5*time.Minute); err != nil {
			harukiLogger.Errorf("Failed to set redis cache: %v", err)
			reason = "save_user_mapping_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to save userID mapping")
		}
		statusTokenMappingKey := harukiRedis.BuildSocialPlatformStatusTokenKey(string(req.Platform), req.UserID)
		if err := apiHelper.DBManager.Redis.SetCache(ctx, statusTokenMappingKey, statusToken, 5*time.Minute); err != nil {
			harukiLogger.Errorf("Failed to set redis cache: %v", err)
			reason = "save_status_mapping_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to save status token mapping")
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
		userID := c.Locals("userID").(string)
		statusTokenKey := harukiRedis.BuildStatusTokenKey(statusToken)
		var status string
		found, err := apiHelper.DBManager.Redis.GetCache(ctx, statusTokenKey, &status)
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
			info, err := apiHelper.DBManager.DB.SocialPlatformInfo.Query().Where(socialplatforminfo.HasUserWith(user.IDEQ(userID))).Only(ctx)
			if err != nil {
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

func handleClearSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID := c.Locals("userID").(string)
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			writeUserAuditLog(c, apiHelper, "user.social_platform.clear", result, userID, map[string]any{
				"reason": reason,
			})
		}()

		exists, err := apiHelper.DBManager.DB.SocialPlatformInfo.
			Query().
			Where(socialplatforminfo.HasUserWith(user.IDEQ(userID))).
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
			Where(socialplatforminfo.HasUserWith(user.IDEQ(userID))).
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
			writeUserAuditLog(c, apiHelper, "user.social_platform.verify_bot", result, targetUserID, map[string]any{
				"reason": reason,
			})
		}()

		authHeader := c.Get("Authorization")
		if len(authHeader) < 7 || authHeader[:7] != "Bearer " {
			reason = "missing_authorization"
			return harukiAPIHelper.ErrorUnauthorized(c, "invalid authorization")
		}
		token := authHeader[7:]
		if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(config.Cfg.UserSystem.SocialPlatformVerifyToken)) != 1 {
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
			return harukiAPIHelper.ErrorBadRequest(c, "this social platform account is already bound")
		}

		if _, err := apiHelper.DBManager.DB.SocialPlatformInfo.
			Create().
			SetPlatform(string(req.Platform)).
			SetPlatformUserID(req.UserID).
			SetVerified(true).
			SetUserID(userID).
			Save(ctx); err != nil {
			harukiLogger.Errorf("Failed to create social platform info: %v", err)
			reason = "create_social_platform_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to update social platform info")
		}

		statusTokenKey := harukiRedis.BuildStatusTokenKey(statusToken)
		if err := apiHelper.DBManager.Redis.SetCache(ctx, statusTokenKey, "true", 5*time.Minute); err != nil {
			harukiLogger.Errorf("Failed to set redis cache: %v", err)
			reason = "set_status_token_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to save status token")
		}

		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.SuccessResponse[string](c, "social platform verified", nil)
	}
}

func registerSocialPlatformRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	social := apiHelper.Router.Group("/api/user/:toolbox_user_id/social-platform")

	social.Post("/send-qq-mail", apiHelper.SessionHandler.VerifySessionToken, checkUserNotBanned(apiHelper), handleSendQQMail(apiHelper))
	social.Post("/verify-qq-mail", apiHelper.SessionHandler.VerifySessionToken, checkUserNotBanned(apiHelper), handleVerifyQQMail(apiHelper))
	social.Post("/generate-verification-code", apiHelper.SessionHandler.VerifySessionToken, checkUserNotBanned(apiHelper), handleGenerateVerificationCode(apiHelper))
	social.Get("/verification-status/:status_token", apiHelper.SessionHandler.VerifySessionToken, checkUserNotBanned(apiHelper), handleVerificationStatus(apiHelper))
	social.Delete("/clear", apiHelper.SessionHandler.VerifySessionToken, checkUserNotBanned(apiHelper), handleClearSocialPlatform(apiHelper))

	apiHelper.Router.Post("/api/verify-social-platform", handleVerifySocialPlatform(apiHelper))
}

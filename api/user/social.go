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
		var req harukiAPIHelper.VerifyQQMailPayload
		if err := c.Bind().Body(&req); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		email := fmt.Sprintf("%s@qq.com", req.QQ)
		ok, err := VerifyEmailHandler(c, email, req.OneTimePassword, apiHelper)
		if err != nil {
			return err
		}
		if !ok {
			return harukiAPIHelper.ErrorBadRequest(c, "verification failed")
		}
		userID := c.Locals("userID").(string)
		if _, err := apiHelper.DBManager.DB.SocialPlatformInfo.
			Create().
			SetPlatform("qq").
			SetPlatformUserID(req.QQ).
			SetVerified(true).
			SetUserID(userID).
			Save(ctx); err != nil {
			harukiLogger.Errorf("Failed to create social platform info: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to update social platform info")
		}

		ud := harukiAPIHelper.HarukiToolboxUserData{
			SocialPlatformInfo: &harukiAPIHelper.SocialPlatformInfo{
				Platform: string(harukiAPIHelper.SocialPlatformQQ),
				UserID:   req.QQ,
				Verified: true,
			},
		}
		return harukiAPIHelper.SuccessResponse(c, "social platform verified", &ud)
	}
}

func handleGenerateVerificationCode(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID := c.Locals("userID").(string)
		var req harukiAPIHelper.GenerateSocialPlatformCodePayload
		if err := c.Bind().Body(&req); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		exists, err := apiHelper.DBManager.DB.SocialPlatformInfo.Query().
			Where(socialplatforminfo.PlatformEQ(string(req.Platform)),
				socialplatforminfo.PlatformUserID(req.UserID)).
			Exist(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to query social platform info: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to query database")
		}
		if exists {
			return harukiAPIHelper.ErrorBadRequest(c, "binding already exists")
		}
		code := GenerateCode(false)
		storageKey := harukiRedis.BuildSocialPlatformVerifyKey(string(req.Platform), req.UserID)
		statusToken := uuid.NewString()
		if err := apiHelper.DBManager.Redis.SetCache(ctx, storageKey, code, 5*time.Minute); err != nil {
			harukiLogger.Errorf("Failed to set redis cache: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to save code")
		}
		statusTokenKey := harukiRedis.BuildStatusTokenKey(statusToken)
		if err := apiHelper.DBManager.Redis.SetCache(ctx, statusTokenKey, "false", 5*time.Minute); err != nil {
			harukiLogger.Errorf("Failed to set redis cache: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to save status token")
		}
		userIDKey := harukiRedis.BuildSocialPlatformUserIDKey(string(req.Platform), req.UserID)
		if err := apiHelper.DBManager.Redis.SetCache(ctx, userIDKey, userID, 5*time.Minute); err != nil {
			harukiLogger.Errorf("Failed to set redis cache: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to save userID mapping")
		}
		statusTokenMappingKey := harukiRedis.BuildSocialPlatformStatusTokenKey(string(req.Platform), req.UserID)
		if err := apiHelper.DBManager.Redis.SetCache(ctx, statusTokenMappingKey, statusToken, 5*time.Minute); err != nil {
			harukiLogger.Errorf("Failed to set redis cache: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to save status token mapping")
		}

		resp := harukiAPIHelper.GenerateSocialPlatformCodeResponse{
			Status:          fiber.StatusOK,
			Message:         "ok",
			StatusToken:     statusToken,
			OneTimePassword: code,
		}
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

		exists, err := apiHelper.DBManager.DB.SocialPlatformInfo.
			Query().
			Where(socialplatforminfo.HasUserWith(user.IDEQ(userID))).
			Exist(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to query social platform info: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to query social platform info")
		}
		if !exists {
			return harukiAPIHelper.ErrorBadRequest(c, "no social platform info found")
		}

		_, err = apiHelper.DBManager.DB.SocialPlatformInfo.
			Delete().
			Where(socialplatforminfo.HasUserWith(user.IDEQ(userID))).
			Exec(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to delete social platform info: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to clear social platform info")
		}

		return harukiAPIHelper.SuccessResponse[string](c, "social platform info cleared successfully", nil)
	}
}

func handleVerifySocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		authHeader := c.Get("Authorization")
		if len(authHeader) < 7 || authHeader[:7] != "Bearer " {
			return harukiAPIHelper.ErrorUnauthorized(c, "invalid authorization")
		}
		token := authHeader[7:]
		if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(config.Cfg.UserSystem.SocialPlatformVerifyToken)) != 1 {
			return harukiAPIHelper.ErrorUnauthorized(c, "invalid authorization")
		}

		var req harukiAPIHelper.HarukiBotVerifySocialPlatformPayload
		if err := c.Bind().Body(&req); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}

		storageKey := harukiRedis.BuildSocialPlatformVerifyKey(string(req.Platform), req.UserID)
		var code string
		found, err := apiHelper.DBManager.Redis.GetCache(ctx, storageKey, &code)
		if err != nil {
			harukiLogger.Errorf("Failed to get redis cache: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to get verification key")
		}
		if !found {
			return harukiAPIHelper.ErrorBadRequest(c, "verification key expired or not found")
		}
		if req.OneTimePassword != code {
			return harukiAPIHelper.ErrorUnauthorized(c, "invalid one time password")
		}

		userIDKey := harukiRedis.BuildSocialPlatformUserIDKey(string(req.Platform), req.UserID)
		var userID string
		found, err = apiHelper.DBManager.Redis.GetCache(ctx, userIDKey, &userID)
		if err != nil {
			harukiLogger.Errorf("Failed to get redis cache: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to get userID")
		}
		if !found {
			return harukiAPIHelper.ErrorBadRequest(c, "userID mapping expired or not found")
		}
		statusTokenMappingKey := harukiRedis.BuildSocialPlatformStatusTokenKey(string(req.Platform), req.UserID)
		var statusToken string
		found, err = apiHelper.DBManager.Redis.GetCache(ctx, statusTokenMappingKey, &statusToken)
		if err != nil {
			harukiLogger.Errorf("Failed to get redis cache: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to get status token")
		}
		if !found {
			return harukiAPIHelper.ErrorBadRequest(c, "status token mapping expired or not found")
		}

		if _, err := apiHelper.DBManager.DB.SocialPlatformInfo.
			Create().
			SetPlatform(string(req.Platform)).
			SetPlatformUserID(req.UserID).
			SetVerified(true).
			SetUserID(userID).
			Save(ctx); err != nil {
			harukiLogger.Errorf("Failed to create social platform info: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to update social platform info")
		}

		statusTokenKey := harukiRedis.BuildStatusTokenKey(statusToken)
		if err := apiHelper.DBManager.Redis.SetCache(ctx, statusTokenKey, "true", 5*time.Minute); err != nil {
			harukiLogger.Errorf("Failed to set redis cache: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to save status token")
		}

		return harukiAPIHelper.SuccessResponse[string](c, "social platform verified", nil)
	}
}

func registerSocialPlatformRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	social := apiHelper.Router.Group("/api/user/:toolbox_user_id/social-platform")

	social.Post("/send-qq-mail", apiHelper.SessionHandler.VerifySessionToken, handleSendQQMail(apiHelper))
	social.Post("/verify-qq-mail", apiHelper.SessionHandler.VerifySessionToken, handleVerifyQQMail(apiHelper))
	social.Post("/generate-verification-code", apiHelper.SessionHandler.VerifySessionToken, handleGenerateVerificationCode(apiHelper))
	social.Get("/verification-status/:status_token", apiHelper.SessionHandler.VerifySessionToken, handleVerificationStatus(apiHelper))
	social.Delete("/clear", apiHelper.SessionHandler.VerifySessionToken, handleClearSocialPlatform(apiHelper))

	apiHelper.Router.Post("/api/verify-social-platform", handleVerifySocialPlatform(apiHelper))
}

package user

import (
	"context"
	"fmt"
	"haruki-suite/config"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql/socialplatforminfo"
	"haruki-suite/utils/database/postgresql/user"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

func handleSendQQMail(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req harukiAPIHelper.SendQQMailPayload
		if err := c.Bind().Body(&req); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid request body", nil)
		}
		ctx := context.Background()
		exists, err := apiHelper.DBManager.DB.SocialPlatformInfo.Query().
			Where(socialplatforminfo.PlatformEQ(
				string(harukiAPIHelper.SocialPlatformQQ)),
				socialplatforminfo.PlatformUserID(req.QQ)).
			Exist(ctx)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to query database", nil)
		}
		if exists {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "QQ binding already exists", nil)
		}
		email := fmt.Sprintf("%s@qq.com", req.QQ)
		return SendEmailHandler(c, email, req.ChallengeToken, apiHelper)
	}
}

func handleVerifyQQMail(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req harukiAPIHelper.VerifyQQMailPayload
		if err := c.Bind().Body(&req); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid request body", nil)
		}
		email := fmt.Sprintf("%s@qq.com", req.QQ)
		ok, err := VerifyEmailHandler(c, email, req.OneTimePassword, apiHelper)
		if err != nil {
			return err
		}
		if !ok {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "verification failed", nil)
		}
		ctx := context.Background()
		userID := c.Locals("userID").(string)
		if _, err := apiHelper.DBManager.DB.SocialPlatformInfo.
			Create().
			SetPlatform("qq").
			SetPlatformUserID(req.QQ).
			SetVerified(true).
			SetUserID(userID).
			Save(ctx); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to update social platform info", nil)
		}

		ud := harukiAPIHelper.HarukiToolboxUserData{
			SocialPlatformInfo: &harukiAPIHelper.SocialPlatformInfo{
				Platform: string(harukiAPIHelper.SocialPlatformQQ),
				UserID:   req.QQ,
				Verified: true,
			},
		}
		return harukiAPIHelper.UpdatedDataResponse(c, fiber.StatusOK, "social platform verified", &ud)
	}
}

func handleGenerateVerificationCode(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := context.Background()
		userID := c.Locals("userID").(string)
		var req harukiAPIHelper.GenerateSocialPlatformCodePayload
		if err := c.Bind().Body(&req); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid request body", nil)
		}
		exists, err := apiHelper.DBManager.DB.SocialPlatformInfo.Query().
			Where(socialplatforminfo.PlatformEQ(string(req.Platform)),
				socialplatforminfo.PlatformUserID(req.UserID)).
			Exist(ctx)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to query database", nil)
		}
		if exists {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "binding already exists", nil)
		}
		code := GenerateCode(false)
		storageKey := fmt.Sprintf("%s:verify:%s", req.Platform, req.UserID)
		statusToken := uuid.NewString()
		if err := apiHelper.DBManager.Redis.SetCache(ctx, storageKey, code, 5*time.Minute); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to save code", nil)
		}
		if err := apiHelper.DBManager.Redis.SetCache(ctx, statusToken, "false", 5*time.Minute); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to save status token", nil)
		}
		if err := apiHelper.DBManager.Redis.SetCache(ctx, storageKey+":"+"userID", userID, 5*time.Minute); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to save userID mapping", nil)
		}
		if err := apiHelper.DBManager.Redis.SetCache(ctx, storageKey+":"+"statusToken", statusToken, 5*time.Minute); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to save status token mapping", nil)
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
		statusToken := c.Params("status_token")
		userID := c.Locals("userID").(string)
		ctx := context.Background()
		var status string
		found, err := apiHelper.DBManager.Redis.GetCache(ctx, statusToken, &status)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to get status", nil)
		}
		if !found {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "status token expired or not found", nil)
		}
		if status == "false" {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "You have not verified yet", nil)
		}
		if status == "true" {
			info, err := apiHelper.DBManager.DB.SocialPlatformInfo.Query().Where(socialplatforminfo.HasUserWith(user.IDEQ(userID))).Only(ctx)
			if err != nil {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to get social platform info", nil)
			}
			ud := harukiAPIHelper.HarukiToolboxUserData{
				SocialPlatformInfo: &harukiAPIHelper.SocialPlatformInfo{
					Platform: info.Platform,
					UserID:   info.PlatformUserID,
					Verified: info.Verified,
				},
			}
			return harukiAPIHelper.UpdatedDataResponse(c, fiber.StatusOK, "verification completed", &ud)
		}
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "get status failed", nil)
	}
}

func handleClearSocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := context.Background()
		userID := c.Locals("userID").(string)

		exists, err := apiHelper.DBManager.DB.SocialPlatformInfo.
			Query().
			Where(socialplatforminfo.HasUserWith(user.IDEQ(userID))).
			Exist(ctx)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to query social platform info", nil)
		}
		if !exists {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "no social platform info found", nil)
		}

		_, err = apiHelper.DBManager.DB.SocialPlatformInfo.
			Delete().
			Where(socialplatforminfo.HasUserWith(user.IDEQ(userID))).
			Exec(ctx)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to clear social platform info", nil)
		}

		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusOK, "social platform info cleared successfully", nil)
	}
}

func handleVerifySocialPlatform(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := context.Background()
		authHeader := c.Get("Authorization")
		if len(authHeader) < 7 || authHeader[:7] != "Bearer " {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid authorization", nil)
		}
		token := authHeader[7:]
		if token == "" || token != config.Cfg.UserSystem.SocialPlatformVerifyToken {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid authorization", nil)
		}

		var req harukiAPIHelper.HarukiBotVerifySocialPlatformPayload
		if err := c.Bind().Body(&req); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid request body", nil)
		}

		storageKey := fmt.Sprintf("%s:verify:%s", string(req.Platform), req.UserID)
		var code string
		found, err := apiHelper.DBManager.Redis.GetCache(ctx, storageKey, &code)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to get verification key", nil)
		}
		if !found {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "verification key expired or not found", nil)
		}
		if req.OneTimePassword != code {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid one time password", nil)
		}

		var userID string
		found, err = apiHelper.DBManager.Redis.GetCache(ctx, storageKey+":"+"userID", &userID)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to get userID", nil)
		}
		if !found {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "userID mapping expired or not found", nil)
		}
		var statusToken string
		found, err = apiHelper.DBManager.Redis.GetCache(ctx, storageKey+":"+"statusToken", &statusToken)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to get status token", nil)
		}
		if !found {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "status token mapping expired or not found", nil)
		}

		if _, err := apiHelper.DBManager.DB.SocialPlatformInfo.
			Create().
			SetPlatform(string(req.Platform)).
			SetPlatformUserID(req.UserID).
			SetVerified(true).
			SetUserID(userID).
			Save(ctx); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to update social platform info", nil)
		}

		if err := apiHelper.DBManager.Redis.SetCache(ctx, statusToken, "true", 5*time.Minute); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to save status token", nil)
		}

		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusOK, "social platform verified", nil)
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

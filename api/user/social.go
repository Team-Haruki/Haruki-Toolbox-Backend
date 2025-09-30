package user

import (
	"context"
	"fmt"
	"haruki-suite/config"
	"haruki-suite/utils/database/postgresql/socialplatforminfo"
	"haruki-suite/utils/database/postgresql/user"
	"haruki-suite/utils/database/redis"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func RegisterSocialPlatformRoutes(helper HarukiToolboxUserRouterHelpers) {
	social := helper.Router.Group("/api/user/:toolbox_user_id/social-platform")

	social.Post("/send-qq-mail", helper.SessionHandler.VerifySessionToken, func(c *fiber.Ctx) error {
		var req SendQQMailPayload
		ctx := context.Background()
		exists, err := helper.DBClient.SocialPlatformInfo.Query().
			Where(socialplatforminfo.PlatformEQ(
				string(SocialPlatformQQ)),
				socialplatforminfo.PlatformUserID(req.QQ)).
			Exist(ctx)
		if err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to query database", nil)
		}
		if exists {
			return UpdatedDataResponse[string](c, fiber.StatusBadRequest, "QQ binding already exists", nil)
		}
		email := fmt.Sprintf("%s@qq.com", req.QQ)
		if err := c.BodyParser(&req); err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid request body", nil)
		}
		return SendEmailHandler(c, email, req.ChallengeToken, helper)

	})

	social.Post("/verify-qq-mail", helper.SessionHandler.VerifySessionToken, func(c *fiber.Ctx) error {
		var req VerifyQQMailPayload
		email := fmt.Sprintf("%s@qq.com", req.QQ)
		if err := c.BodyParser(&req); err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid request body", nil)
		}
		result := VerifyEmailHandler(c, email, req.OneTimePassword, helper)
		if result != nil {
			return result
		}
		ctx := context.Background()
		userID := c.Locals("userID").(string)
		if _, err := helper.DBClient.SocialPlatformInfo.
			Update().
			Where(socialplatforminfo.HasUserWith(user.IDEQ(userID))).
			SetPlatform(string(SocialPlatformQQ)).
			SetUserID(req.QQ).
			SetVerified(true).
			Save(ctx); err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to update social platform info", nil)
		}

		ud := HarukiToolboxUserData{
			SocialPlatformInfo: &SocialPlatformInfo{
				Platform: string(SocialPlatformQQ),
				UserID:   req.QQ,
				Verified: true,
			},
		}
		return UpdatedDataResponse(c, fiber.StatusOK, "social platform verified", &ud)
	})

	social.Post("/generate-verification-code", helper.SessionHandler.VerifySessionToken, func(c *fiber.Ctx) error {
		ctx := context.Background()
		userID := c.Locals("userID").(string)
		var req GenerateSocialPlatformCodePayload
		if err := c.BodyParser(&req); err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid request body", nil)
		}
		code := GenerateCode(false)
		storageKey := fmt.Sprintf("%s:verify:%s", req.Platform, req.UserID)
		statusToken := uuid.NewString()
		if err := redis.SetCache(ctx, helper.RedisClient, storageKey, code, 5*time.Minute); err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to save code", nil)
		}
		if err := redis.SetCache(ctx, helper.RedisClient, statusToken, "false", 5*time.Minute); err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to save status token", nil)
		}
		if err := redis.SetCache(ctx, helper.RedisClient, storageKey+":"+"userID", userID, 5*time.Minute); err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to save userID mapping", nil)
		}
		if err := redis.SetCache(ctx, helper.RedisClient, storageKey+":"+"statusToken", statusToken, 5*time.Minute); err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to save status token mapping", nil)
		}

		resp := GenerateSocialPlatformCodeResponse{
			Status:          fiber.StatusOK,
			Message:         "ok",
			StatusToken:     statusToken,
			OneTimePassword: code,
		}
		return ResponseWithStruct(c, fiber.StatusOK, resp)
	})

	social.Get("/verification-status/:status_token", helper.SessionHandler.VerifySessionToken, func(c *fiber.Ctx) error {
		statusToken := c.Params("status_token")
		userID := c.Locals("userID").(string)
		ctx := context.Background()
		var status string
		found, err := redis.GetCache(ctx, helper.RedisClient, statusToken, &status)
		if err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to get status", nil)
		}
		if !found {
			return UpdatedDataResponse[string](c, fiber.StatusBadRequest, "status token expired or not found", nil)
		}
		if status == "false" {
			return UpdatedDataResponse[string](c, fiber.StatusBadRequest, "You have not verified yet", nil)
		}
		if status == "true" {
			info, err := helper.DBClient.SocialPlatformInfo.Query().Where(socialplatforminfo.HasUserWith(user.IDEQ(userID))).Only(ctx)
			if err != nil {
				return UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to get social platform info", nil)
			}
			ud := HarukiToolboxUserData{
				SocialPlatformInfo: &SocialPlatformInfo{
					Platform: info.Platform,
					UserID:   info.PlatformUserID,
					Verified: info.Verified,
				},
			}
			return UpdatedDataResponse(c, fiber.StatusBadRequest, "invalid status", &ud)
		}
		return UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "get status failed", nil)
	})

	helper.Router.Post("/api/verify-social-platform", func(c *fiber.Ctx) error {
		ctx := context.Background()
		authHeader := c.Get("Authorization")
		if len(authHeader) < 7 || authHeader[:7] != "Bearer " {
			return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid authorization", nil)
		}
		token := authHeader[7:]
		if token == "" || token != config.Cfg.UserSystem.SocialPlatformVerifyToken {
			return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid authorization", nil)
		}

		var req HarukiBotVerifySocialPlatformPayload
		if err := c.BodyParser(&req); err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid request body", nil)
		}

		storageKey := fmt.Sprintf("%s:verify:%s", req.Platform, req.UserID)
		var code string
		found, err := redis.GetCache(ctx, helper.RedisClient, storageKey, &code)
		if err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to get verification key", nil)
		}
		if !found {
			return UpdatedDataResponse[string](c, fiber.StatusBadRequest, "verification key expired or not found", nil)
		}
		if req.OneTimePassword != code {
			return UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "invalid one time password", nil)
		}

		var userID string
		found, err = redis.GetCache(ctx, helper.RedisClient, storageKey+":"+"userID", &userID)
		if err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to get userID", nil)
		}
		if !found {
			return UpdatedDataResponse[string](c, fiber.StatusBadRequest, "userID mapping expired or not found", nil)
		}
		var statusToken string
		found, err = redis.GetCache(ctx, helper.RedisClient, storageKey+":"+"statusToken", &statusToken)
		if err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to get status token", nil)
		}
		if !found {
			return UpdatedDataResponse[string](c, fiber.StatusBadRequest, "status token mapping expired or not found", nil)
		}

		if _, err := helper.DBClient.SocialPlatformInfo.
			Update().
			Where(socialplatforminfo.HasUserWith(user.IDEQ(userID))).
			SetPlatform(string(req.Platform)).
			SetUserID(req.UserID).
			SetVerified(true).
			Save(ctx); err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to update social platform info", nil)
		}

		if err := redis.SetCache(ctx, helper.RedisClient, statusToken, "true", 5*time.Minute); err != nil {
			return UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "failed to save status token", nil)
		}

		return UpdatedDataResponse[string](c, fiber.StatusOK, "social platform verified", nil)
	})

}

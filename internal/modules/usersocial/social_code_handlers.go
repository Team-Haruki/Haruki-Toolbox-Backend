package usersocial

import (
	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	userEmailModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/useremail"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/socialplatforminfo"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

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
			{Key: harukiRedis.BuildSocialPlatformVerifyAttemptKey(string(req.Platform), req.UserID), Value: 0},
		}, socialPlatformVerifyTTL); err != nil {
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

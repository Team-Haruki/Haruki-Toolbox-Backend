package usersocial

import (
	"fmt"
	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	userEmailModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/useremail"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/socialplatforminfo"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func handleSendQQMail(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		var req harukiAPIHelper.SendQQMailPayload
		if err := c.Bind().Body(&req); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		req.QQ = strings.TrimSpace(req.QQ)
		if req.QQ == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "qq is required")
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
		limited, limitKey, limitMessage, err := checkQQMailSendRateLimit(c, apiHelper, userID, req.QQ)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "verification service unavailable")
		}
		if limited {
			return respondQQMailRateLimited(c, limitKey, limitMessage, apiHelper)
		}
		email := fmt.Sprintf("%s@qq.com", req.QQ)
		if err := userEmailModule.SendEmailHandler(c, email, req.ChallengeToken, apiHelper); err != nil {
			if releaseErr := releaseQQMailSendRateLimitReservation(c, apiHelper, userID, req.QQ); releaseErr != nil {
				harukiLogger.Warnf("Failed to release QQ mail send rate limit reservation for %s/%s: %v", userID, req.QQ, releaseErr)
			}
			return err
		}
		return nil
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
		req.QQ = strings.TrimSpace(req.QQ)
		if req.QQ == "" {
			reason = "invalid_payload"
			return harukiAPIHelper.ErrorBadRequest(c, "qq is required")
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

package usersocial

import (
	"errors"
	"strings"

	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	userEmailModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/useremail"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiCloudflare "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/cloudflare"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/socialplatforminfo"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
)

func handleSendQQMail(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, turnstileVerifier harukiCloudflare.Verifier) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		var req harukiAPIHelper.SendQQMailPayload
		if err := c.Bind().Body(&req); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		req.QQ = strings.TrimSpace(req.QQ)
		if !validQQMailID(req.QQ) {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid QQ number")
		}
		if err := userEmailModule.SendEmailHandler(c, userID, req.QQ+"@qq.com", req.ChallengeToken, apiHelper, turnstileVerifier); err != nil {
			return respondQQVerificationError(c, err)
		}
		return harukiAPIHelper.Responses.SuccessResponse[string](c, "verification code sent", nil)
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
		if !validQQMailID(req.QQ) {
			reason = "invalid_payload"
			return harukiAPIHelper.ErrorBadRequest(c, "invalid QQ number")
		}
		if err := userEmailModule.VerifyEmailHandler(c, userID, req.QQ+"@qq.com", req.OneTimePassword, apiHelper); err != nil {
			reason = "verify_email_otp_failed"
			return respondQQVerificationError(c, err)
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
			return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusConflict, "QQ binding already exists", nil)
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
				return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusConflict, "QQ binding already exists", nil)
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
		return harukiAPIHelper.Responses.SuccessResponse(c, "social platform verified", &ud)
	}
}

func validQQMailID(id string) bool {
	if len(id) == 0 || len(id) > 20 || id[0] == '0' {
		return false
	}
	for i := range len(id) {
		if id[i] < '0' || id[i] > '9' {
			return false
		}
	}
	return true
}

func respondQQVerificationError(c fiber.Ctx, err error) error {
	var requestErr *fiber.Error
	if errors.As(err, &requestErr) {
		return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, requestErr.Code, requestErr.Message, nil)
	}
	return harukiAPIHelper.ErrorInternal(c, "verification service unavailable")
}

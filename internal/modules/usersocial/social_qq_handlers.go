package usersocial

import (
	"context"
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

var (
	errQQOwnedByAnotherUser = errors.New("QQ binding already exists")
	errQQReplacement        = errors.New("unlink the existing social account before binding a different QQ number")
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
		err = userEmailModule.VerifyEmailHandler(c, userID, req.QQ+"@qq.com", req.OneTimePassword, apiHelper, func() error {
			return verifyOwnedQQBinding(ctx, apiHelper.DBManager.DB, userID, req.QQ)
		})
		if errors.Is(err, errQQOwnedByAnotherUser) {
			reason = "social_platform_conflict"
			return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusConflict, errQQOwnedByAnotherUser.Error(), nil)
		}
		if errors.Is(err, errQQReplacement) {
			reason = "existing_social_link"
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}
		if err != nil {
			var verificationErr *fiber.Error
			if errors.As(err, &verificationErr) {
				reason = "verify_email_otp_failed"
				return respondQQVerificationError(c, err)
			}
			harukiLogger.Errorf("Failed to verify social platform info: %v", err)
			reason = "verify_social_platform_failed"
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

func verifyOwnedQQBinding(ctx context.Context, db *postgresql.Client, userID, qq string) error {
	release, err := db.LockUserMutations(ctx, userID)
	if err != nil {
		return err
	}
	defer release()

	const platform = string(harukiAPIHelper.SocialPlatformQQ)
	query := db.SocialPlatformInfo.Query().Where(socialplatforminfo.PlatformEQ(platform), socialplatforminfo.PlatformUserIDEQ(qq))
	row, err := query.Clone().Only(ctx)
	if postgresql.IsNotFound(err) {
		linked, err := db.SocialPlatformInfo.Query().Where(socialplatforminfo.UserSocialPlatformInfoEQ(userID)).Exist(ctx)
		if err != nil {
			return err
		}
		if linked {
			return errQQReplacement
		}
		err = db.SocialPlatformInfo.Create().SetPlatform(platform).SetPlatformUserID(qq).SetVerified(true).SetUserID(userID).Exec(ctx)
		if postgresql.IsConstraintError(err) {
			other, lookupErr := query.Where(socialplatforminfo.UserSocialPlatformInfoNEQ(userID)).Exist(ctx)
			if lookupErr == nil && other {
				return errQQOwnedByAnotherUser
			}
		}
		return err
	}
	if err != nil {
		return err
	}
	if row.UserSocialPlatformInfo != userID {
		return errQQOwnedByAnotherUser
	}
	if row.Verified {
		return nil
	}
	return db.SocialPlatformInfo.UpdateOneID(row.ID).
		Where(socialplatforminfo.UserSocialPlatformInfoEQ(userID), socialplatforminfo.PlatformEQ(platform), socialplatforminfo.PlatformUserIDEQ(qq)).
		SetVerified(true).Exec(ctx)
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

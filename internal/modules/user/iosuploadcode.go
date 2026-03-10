package user

import (
	"crypto/rand"
	"encoding/hex"
	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/iosscriptcode"
	"haruki-suite/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v3"
)

func GenerateUploadCode() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func handleGenerateIOSUploadCode(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID := c.Params("toolbox_user_id")
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.ios_upload_code.regenerate", result, userID, map[string]any{
				"reason": reason,
			})
		}()

		if userID == "" {
			reason = "missing_user_id"
			return harukiAPIHelper.ErrorBadRequest(c, "missing user_id")
		}
		_, err := apiHelper.DBManager.DB.User.Query().Where(user.IDEQ(userID)).Only(ctx)
		if err != nil {
			if postgresql.IsNotFound(err) {
				reason = "user_not_found"
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			reason = "query_user_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to query user")
		}
		newCode, err := GenerateUploadCode()
		if err != nil {
			reason = "generate_upload_code_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to generate upload code")
		}
		existing, err := apiHelper.DBManager.DB.IOSScriptCode.Query().
			Where(iosscriptcode.UserIDEQ(userID)).
			Only(ctx)
		if err == nil {
			_, err = existing.Update().SetUploadCode(newCode).Save(ctx)
			if err != nil {
				reason = "update_upload_code_failed"
				return harukiAPIHelper.ErrorInternal(c, "failed to update upload code")
			}
		} else {
			if !postgresql.IsNotFound(err) {
				reason = "query_upload_code_failed"
				return harukiAPIHelper.ErrorInternal(c, "failed to query upload code")
			}
			_, err = apiHelper.DBManager.DB.IOSScriptCode.Create().
				SetUserID(userID).
				SetUploadCode(newCode).
				Save(ctx)
			if err != nil {
				reason = "create_upload_code_failed"
				return harukiAPIHelper.ErrorInternal(c, "failed to create upload code")
			}
		}
		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.SuccessResponse(c, "upload code generated successfully", &newCode)
	}
}

func RegisterUserRoutes(helper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	api := helper.Router.Group("/api/user")
	api.Post("/:toolbox_user_id/ios/generate-upload-code", helper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(helper), handleGenerateIOSUploadCode(helper))

	// Legacy compatibility route. Keep temporarily to avoid breaking old clients.
	legacy := helper.Router.Group("/user")
	legacy.Post("/:toolbox_user_id/ios/generate-upload-code", helper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(helper), handleGenerateIOSUploadCode(helper))
}

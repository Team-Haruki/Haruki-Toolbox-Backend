package user

import (
	"crypto/rand"
	"encoding/hex"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql/iosscriptcode"
	"haruki-suite/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v3"
)

func generateUploadCode() (string, error) {
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
		if userID == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "missing user_id")
		}
		_, err := apiHelper.DBManager.DB.User.Query().Where(user.IDEQ(userID)).Only(ctx)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "user not found")
		}
		newCode, err := generateUploadCode()
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to generate upload code")
		}
		existing, err := apiHelper.DBManager.DB.IOSScriptCode.Query().
			Where(iosscriptcode.UserIDEQ(userID)).
			Only(ctx)
		if err == nil {
			_, err = existing.Update().SetUploadCode(newCode).Save(ctx)
			if err != nil {
				return harukiAPIHelper.ErrorInternal(c, "failed to update upload code")
			}
		} else {
			_, err = apiHelper.DBManager.DB.IOSScriptCode.Create().
				SetUserID(userID).
				SetUploadCode(newCode).
				Save(ctx)
			if err != nil {
				return harukiAPIHelper.ErrorInternal(c, "failed to create upload code")
			}
		}
		return harukiAPIHelper.SuccessResponse(c, "upload code generated successfully", &newCode)
	}
}

func registerIOSUploadCodeRoutes(helper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	api := helper.Router.Group("/user")
	api.Post("/:toolbox_user_id/ios/generate-upload-code", helper.SessionHandler.VerifySessionToken, checkUserNotBanned(helper), handleGenerateIOSUploadCode(helper))
}

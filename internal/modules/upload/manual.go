package upload

import (
	"fmt"
	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"strconv"

	"github.com/gofiber/fiber/v3"
)

func handleManualUpload(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		serverStr := c.Params("server")
		gameUserIDStr := c.Params("user_id")
		dataTypeStr := c.Params("data_type")
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid server")
		}
		dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid data_type")
		}
		gameUserID, err := strconv.ParseInt(gameUserIDStr, 10, 64)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid user_id")
		}
		_, err = HandleUpload(
			ctx,
			c.Request().Body(),
			server,
			dataType,
			&gameUserID,
			&userID,
			apiHelper,
			harukiUtils.UploadMethodManual,
		)
		if err != nil {
			if mapped := mapUploadProcessingError(err); mapped != nil {
				return harukiAPIHelper.UpdatedDataResponse[string](c, mapped.Code, mapped.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "failed to process upload")
		}
		return harukiAPIHelper.SuccessResponse[string](c, fmt.Sprintf("%s server user %d successfully uploaded suite data.", serverStr, gameUserID), nil)
	}
}

func registerManualUploadRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	api := apiHelper.Router.Group("/manual/:server/:user_id/:data_type", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.CheckUserNotBanned(apiHelper))

	api.Post("/upload", handleManualUpload(apiHelper))
}

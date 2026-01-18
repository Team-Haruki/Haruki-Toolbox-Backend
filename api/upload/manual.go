package upload

import (
	"fmt"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"strconv"

	"github.com/gofiber/fiber/v3"
)

func handleManualUpload(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID := c.Locals("userID").(string)
		serverStr := c.Params("server")
		gameUserIDStr := c.Params("user_id")
		dataTypeStr := c.Params("data_type")
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}
		dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}
		gameUserIDInt, err := strconv.Atoi(gameUserIDStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid user_id")
		}
		gameUserID := int64(gameUserIDInt)
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
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}
		return harukiAPIHelper.SuccessResponse[string](c, fmt.Sprintf("%s server user %d successfully uploaded suite data.", serverStr, gameUserID), nil)
	}
}

func registerManualUploadRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	api := apiHelper.Router.Group("/manual/:server/:user_id/:data_type", apiHelper.SessionHandler.VerifySessionToken)

	api.Post("/upload", handleManualUpload(apiHelper))
}

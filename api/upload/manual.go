package upload

import (
	"context"
	"fmt"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"strconv"

	"github.com/gofiber/fiber/v2"
)

func handleManualUpload(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c *fiber.Ctx) error {
		userID := c.Locals("userID").(string)
		serverStr := c.Params("server")
		gameUserIDStr := c.Params("user_id")
		dataTypeStr := c.Params("data_type")

		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
		}
		dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
		}
		gameUserIDInt, err := strconv.Atoi(gameUserIDStr)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "invalid user_id", nil)
		}
		gameUserID := int64(gameUserIDInt)

		_, err = HandleUpload(
			context.Background(),
			c.Request().Body(),
			server,
			dataType,
			&gameUserID,
			&userID,
			apiHelper,
		)

		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
		}

		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusOK, fmt.Sprintf("%s server user %d successfully uploaded suite data.", serverStr, gameUserID), nil)
	}
}

func registerManualUploadRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	api := apiHelper.Router.Group("/manual/:server/:user_id/:data_type", apiHelper.SessionHandler.VerifySessionToken)

	api.Post("/upload", handleManualUpload(apiHelper))
}

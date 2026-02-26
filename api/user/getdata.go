package user

import (
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	"haruki-suite/utils/database/postgresql/user"
	"strconv"

	"haruki-suite/utils/api/data"

	"github.com/gofiber/fiber/v3"
)

func handleGetOwnData(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	allowedKeySet := make(map[string]struct{}, len(apiHelper.PublicAPIAllowedKeys))
	for _, k := range apiHelper.PublicAPIAllowedKeys {
		allowedKeySet[k] = struct{}{}
	}

	return func(c fiber.Ctx) error {
		ctx := c.Context()

		toolboxUserID := c.Params("toolbox_user_id")
		serverStr := c.Params("server")
		dataTypeStr := c.Params("data_type")
		gameUserIDStr := c.Params("user_id")

		authUserID := c.Locals("userID").(string)
		if authUserID != toolboxUserID {
			return harukiAPIHelper.ErrorForbidden(c, "you can only access your own data")
		}

		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}

		dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}

		gameUserID, err := strconv.ParseInt(gameUserIDStr, 10, 64)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "Invalid game user_id, it must be integer")
		}

		binding, err := apiHelper.DBManager.DB.GameAccountBinding.
			Query().
			Where(
				gameaccountbinding.ServerEQ(string(server)),
				gameaccountbinding.GameUserIDEQ(gameUserIDStr),
				gameaccountbinding.HasUserWith(user.IDEQ(toolboxUserID)),
			).
			Only(ctx)

		if err != nil || binding == nil {
			return harukiAPIHelper.ErrorNotFound(c, "game account binding not found or not owned by you")
		}

		if !binding.Verified {
			return harukiAPIHelper.ErrorForbidden(c, "game account binding is not verified")
		}

		requestKey := c.Query("key")
		var resp any

		if dataType == harukiUtils.UploadDataTypeSuite {
			resp, err = data.HandleSuiteRequest(c, apiHelper, gameUserID, server, requestKey, allowedKeySet, apiHelper.PublicAPIAllowedKeys)
		} else {
			resp, err = data.HandleMysekaiRequest(c, apiHelper, gameUserID, server, requestKey)
		}

		if err != nil {
			if fErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fErr.Code, fErr.Message, nil)
			}
			return harukiAPIHelper.ErrorInternal(c, err.Error())
		}

		return c.JSON(resp)
	}
}

func registerGameDataRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id/game-data")
	r.Get("/:server/:data_type/:user_id", apiHelper.SessionHandler.VerifySessionToken, checkUserNotBanned(apiHelper), handleGetOwnData(apiHelper))
}

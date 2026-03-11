package userinfo

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	userSchema "haruki-suite/utils/database/postgresql/user"
	harukiLogger "haruki-suite/utils/logger"
	"strconv"

	"haruki-suite/utils/api/data"

	"github.com/gofiber/fiber/v3"
)

func mapOwnedBindingLookupError(err error) *fiber.Error {
	if err == nil {
		return nil
	}
	if postgresql.IsNotFound(err) {
		return fiber.NewError(fiber.StatusNotFound, "game account binding not found or not owned by you")
	}
	return fiber.NewError(fiber.StatusInternalServerError, "failed to query game account binding")
}

func handleGetOwnData(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()

		toolboxUserID := c.Params("toolbox_user_id")
		serverStr := c.Params("server")
		dataTypeStr := c.Params("data_type")
		gameUserIDStr := c.Params("user_id")

		authUserID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		if authUserID != toolboxUserID {
			return harukiAPIHelper.ErrorForbidden(c, "you can only access your own data")
		}

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
			return harukiAPIHelper.ErrorBadRequest(c, "Invalid game user_id, it must be integer")
		}

		binding, err := apiHelper.DBManager.DB.GameAccountBinding.
			Query().
			Where(
				gameaccountbinding.ServerEQ(string(server)),
				gameaccountbinding.GameUserIDEQ(gameUserIDStr),
				gameaccountbinding.HasUserWith(userSchema.IDEQ(toolboxUserID)),
			).
			Only(ctx)

		if lookupErr := mapOwnedBindingLookupError(err); lookupErr != nil {
			if lookupErr.Code == fiber.StatusNotFound {
				return harukiAPIHelper.ErrorNotFound(c, lookupErr.Message)
			}
			harukiLogger.Errorf("Failed to query own game account binding: %v", err)
			return harukiAPIHelper.ErrorInternal(c, lookupErr.Message)
		}
		if binding == nil {
			return harukiAPIHelper.ErrorNotFound(c, "game account binding not found or not owned by you")
		}

		if !binding.Verified {
			return harukiAPIHelper.ErrorForbidden(c, "game account binding is not verified")
		}

		requestKey := c.Query("key")
		var resp any
		publicAPIAllowedKeys := apiHelper.GetPublicAPIAllowedKeys()
		allowedKeySet := make(map[string]struct{}, len(publicAPIAllowedKeys))
		for _, k := range publicAPIAllowedKeys {
			allowedKeySet[k] = struct{}{}
		}

		if dataType == harukiUtils.UploadDataTypeSuite {
			resp, err = data.HandleSuiteRequest(c, apiHelper, gameUserID, server, requestKey, allowedKeySet, publicAPIAllowedKeys)
		} else {
			resp, err = data.HandleMysekaiRequest(c, apiHelper, gameUserID, server, requestKey)
		}

		if err != nil {
			if fErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fErr.Code, fErr.Message, nil)
			}
			harukiLogger.Errorf("Failed to load own game data: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to get user data")
		}

		return c.JSON(resp)
	}
}

func registerGameDataRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id/game-data")
	r.Get("/:server/:data_type/:user_id", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleGetOwnData(apiHelper))
}

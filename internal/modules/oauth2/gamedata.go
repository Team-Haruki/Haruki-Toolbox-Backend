package oauth2

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/api/data"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	"haruki-suite/utils/database/postgresql/user"
	harukiLogger "haruki-suite/utils/logger"
	harukiOAuth2 "haruki-suite/utils/oauth2"
	"strconv"

	"github.com/gofiber/fiber/v3"
)

func handleOAuth2GetGameData(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()

		serverStr := c.Params("server")
		dataTypeStr := c.Params("data_type")
		gameUserIDStr := c.Params("user_id")

		authUserID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
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
				gameaccountbinding.HasUserWith(user.IDEQ(authUserID)),
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
			harukiLogger.Errorf("Failed to load OAuth2 game data: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to get user data")
		}

		return c.JSON(resp)
	}
}

func registerOAuth2GameDataRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	o := apiHelper.Router.Group("/api/oauth2/game-data")
	o.Get("/:server/:data_type/:user_id",
		harukiOAuth2.VerifyOAuth2Token(apiHelper.DBManager.DB, harukiOAuth2.ScopeGameDataRead),
		handleOAuth2GetGameData(apiHelper),
	)
}

package oauth2

import (
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/api/data"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	"haruki-suite/utils/database/postgresql/user"
	harukiOAuth2 "haruki-suite/utils/oauth2"
	"strconv"

	"github.com/gofiber/fiber/v3"
)

// handleOAuth2GetGameData handles game data requests authenticated via OAuth2 token.
// The user identity is derived from the token (no toolbox_user_id in URL).
// GET /api/oauth2/game-data/:server/:data_type/:user_id
// Scope: game-data:read
func handleOAuth2GetGameData(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	allowedKeySet := make(map[string]struct{}, len(apiHelper.PublicAPIAllowedKeys))
	for _, k := range apiHelper.PublicAPIAllowedKeys {
		allowedKeySet[k] = struct{}{}
	}

	return func(c fiber.Ctx) error {
		ctx := c.Context()

		serverStr := c.Params("server")
		dataTypeStr := c.Params("data_type")
		gameUserIDStr := c.Params("user_id")

		authUserID := c.Locals("userID").(string)

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

// registerOAuth2GameDataRoutes registers the OAuth2-protected game data endpoint.
func registerOAuth2GameDataRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	o := apiHelper.Router.Group("/api/oauth2/game-data")
	o.Get("/:server/:data_type/:user_id",
		harukiOAuth2.VerifyOAuth2Token(apiHelper.DBManager.DB, harukiOAuth2.ScopeGameDataRead),
		handleOAuth2GetGameData(apiHelper),
	)
}

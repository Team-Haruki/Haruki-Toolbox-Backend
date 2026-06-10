package oauth2

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/api/data"
	harukiRedis "haruki-suite/utils/database/redis"
	harukiLogger "haruki-suite/utils/logger"
	harukiOAuth2 "haruki-suite/utils/oauth2"
	"strconv"
	"time"

	"github.com/bytedance/sonic"
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

		access, err := apiHelper.DBManager.DB.CanAccessGameAccountData(ctx, authUserID, string(server), gameUserIDStr, string(dataType), time.Now().UTC())
		if err != nil {
			harukiLogger.Errorf("Failed to verify oauth2 game account data access: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to query game account binding")
		}
		if access == nil || !access.Allowed {
			return harukiAPIHelper.ErrorNotFound(c, "game account binding not found or not owned by you")
		}

		requestKey := c.Query("key")
		cacheKey := harukiRedis.BuildGameDataCacheKey("oauth2", string(server), string(dataType), gameUserID, requestKey)
		if cached, found, cErr := apiHelper.DBManager.Redis.GetRawCache(ctx, cacheKey); cErr == nil && found {
			c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
			return c.SendString(cached)
		} else if cErr != nil {
			harukiLogger.Warnf("Failed to read OAuth2 game data cache: %v", cErr)
		}

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

		if encoded, mErr := sonic.Marshal(resp); mErr == nil {
			if cErr := apiHelper.DBManager.Redis.SetRawCache(ctx, cacheKey, string(encoded), 300*time.Second); cErr != nil {
				harukiLogger.Warnf("Failed to write OAuth2 game data cache: %v", cErr)
			}
		} else {
			harukiLogger.Warnf("Failed to marshal OAuth2 game data cache: %v", mErr)
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

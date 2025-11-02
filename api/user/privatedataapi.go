package user

import (
	harukiUtils "haruki-suite/utils"
	harukiApiHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/authorizesocialplatforminfo"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
)

func ValidateUserPermission(expectedToken, requiredAgentKeyword string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		authorization := c.Get("Authorization")
		userAgent := c.Get("User-Agent")

		if authorization != expectedToken {
			return harukiApiHelper.UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "unauthorized token", nil)
		}

		if requiredAgentKeyword != "" && !harukiApiHelper.StringContains(userAgent, requiredAgentKeyword) {
			return harukiApiHelper.UpdatedDataResponse[string](c, fiber.StatusUnauthorized, "unauthorized user agent", nil)
		}
		return c.Next()
	}
}

func handleGetPrivateData(apiHelper *harukiApiHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c *fiber.Ctx) error {
		serverStr := c.Params("server")
		dataTypeStr := c.Params("data_type")
		userIDStr := c.Params("user_id")
		platform := c.Query("platform")
		platformUserID := c.Query("platform_user_id")

		if (platform != "" && platformUserID == "") || (platform == "" && platformUserID != "") {
			return harukiApiHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "both platform and platform_user_id must be provided together", nil)
		}

		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiApiHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
		}
		dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
		if err != nil {
			return harukiApiHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
		}
		userID, err := strconv.Atoi(userIDStr)
		if err != nil {
			return harukiApiHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
		}

		gameAccountBinding, err := apiHelper.DBManager.DB.GameAccountBinding.Query().
			Where(
				gameaccountbinding.ServerEQ(string(server)),
				gameaccountbinding.GameUserIDEQ(userIDStr),
			).
			First(c.Context())
		if err != nil {
			return harukiApiHelper.UpdatedDataResponse[string](c, fiber.StatusNotFound, "game account not found", nil)
		}

		dbUser, err := gameAccountBinding.QueryUser().WithSocialPlatformInfo().Only(c.Context())
		if err != nil {
			return harukiApiHelper.UpdatedDataResponse[string](c, fiber.StatusNotFound, "game account not found", nil)
		}

		authorized := isUserAuthorized(c, apiHelper, dbUser, platform, platformUserID)
		if !authorized {
			return harukiApiHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, "forbidden: invalid platform or platform_user_id for this user", nil)
		}

		result, err := apiHelper.DBManager.Mongo.GetData(c.Context(), int64(userID), string(server), dataType)
		if err != nil {
			return harukiApiHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
		}

		if result == nil {
			return harukiApiHelper.UpdatedDataResponse[string](c, fiber.StatusNotFound, "user data not found", nil)
		}

		return processRequestKeys(c, result)
	}
}

func isUserAuthorized(c *fiber.Ctx, apiHelper *harukiApiHelper.HarukiToolboxRouterHelpers, dbUser *postgresql.User, platform, platformUserID string) bool {
	// Check primary social platform info
	if dbUser.Edges.SocialPlatformInfo != nil &&
		dbUser.Edges.SocialPlatformInfo.Platform == platform &&
		dbUser.Edges.SocialPlatformInfo.PlatformUserID == platformUserID {
		return true
	}

	// Check authorized social platforms
	exists, err := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo.Query().
		Where(
			authorizesocialplatforminfo.UserIDEQ(dbUser.ID),
			authorizesocialplatforminfo.PlatformEQ(platform),
			authorizesocialplatforminfo.PlatformUserIDEQ(platformUserID),
		).
		Exist(c.Context())
	if err == nil && exists {
		return true
	}

	return false
}

func processRequestKeys(c *fiber.Ctx, result map[string]interface{}) error {
	requestKey := c.Query("key")
	if requestKey != "" {
		keys := strings.Split(requestKey, ",")
		if len(keys) == 1 {
			return c.JSON(result[keys[0]])
		}
		data := make(map[string]interface{})
		for _, k := range keys {
			data[k] = result[k]
		}
		return c.JSON(data)
	}
	return c.JSON(result)
}

func registerPrivateAPIRoutes(apiHelper *harukiApiHelper.HarukiToolboxRouterHelpers) {
	api := apiHelper.Router.Group("/private/:server/:data_type/:user_id", ValidateUserPermission(apiHelper.PrivateAPIToken, apiHelper.PrivateAPIUserAgent))

	api.Get("/", handleGetPrivateData(apiHelper))
}

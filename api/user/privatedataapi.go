package user

import (
	"crypto/subtle"
	harukiUtils "haruki-suite/utils"
	harukiApiHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/authorizesocialplatforminfo"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func ValidateUserPermission(expectedToken, requiredAgentKeyword string) fiber.Handler {
	return func(c fiber.Ctx) error {
		authorization := c.Get("Authorization")
		userAgent := c.Get("User-Agent")
		if subtle.ConstantTimeCompare([]byte(authorization), []byte(expectedToken)) != 1 {
			return harukiApiHelper.ErrorUnauthorized(c, "unauthorized token")
		}
		if requiredAgentKeyword != "" && !harukiApiHelper.StringContains(userAgent, requiredAgentKeyword) {
			return harukiApiHelper.ErrorUnauthorized(c, "unauthorized user agent")
		}
		return c.Next()
	}
}

func handleGetPrivateData(apiHelper *harukiApiHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		serverStr := c.Params("server")
		dataTypeStr := c.Params("data_type")
		userIDStr := c.Params("user_id")
		platform := c.Query("platform")
		platformUserID := c.Query("platform_user_id")
		if platform == "" || platformUserID == "" {
			return harukiApiHelper.ErrorBadRequest(c, "both platform and platform_user_id are required")
		}
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiApiHelper.ErrorBadRequest(c, err.Error())
		}
		dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
		if err != nil {
			return harukiApiHelper.ErrorBadRequest(c, err.Error())
		}
		userID, err := strconv.Atoi(userIDStr)
		if err != nil {
			return harukiApiHelper.ErrorBadRequest(c, err.Error())
		}
		gameAccountBinding, err := apiHelper.DBManager.DB.GameAccountBinding.Query().
			Where(
				gameaccountbinding.ServerEQ(string(server)),
				gameaccountbinding.GameUserIDEQ(userIDStr),
			).
			First(ctx)
		if err != nil {
			return harukiApiHelper.ErrorNotFound(c, "game account not found")
		}
		dbUser, err := gameAccountBinding.QueryUser().WithSocialPlatformInfo().Only(ctx)
		if err != nil {
			return harukiApiHelper.ErrorNotFound(c, "game account not found")
		}
		authorized := isUserAuthorized(c, apiHelper, dbUser, platform, platformUserID)
		if !authorized {
			return harukiApiHelper.ErrorForbidden(c, "forbidden: invalid platform or platform_user_id for this user")
		}
		result, err := apiHelper.DBManager.Mongo.GetData(ctx, int64(userID), string(server), dataType)
		if err != nil {
			return harukiApiHelper.ErrorBadRequest(c, err.Error())
		}
		if result == nil {
			return harukiApiHelper.ErrorNotFound(c, "user data not found")
		}
		return processRequestKeys(c, result)
	}
}

func isUserAuthorized(c fiber.Ctx, apiHelper *harukiApiHelper.HarukiToolboxRouterHelpers, dbUser *postgresql.User, platform, platformUserID string) bool {
	if dbUser.Edges.SocialPlatformInfo != nil &&
		dbUser.Edges.SocialPlatformInfo.Platform == platform &&
		dbUser.Edges.SocialPlatformInfo.PlatformUserID == platformUserID {
		return true
	}
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

func processRequestKeys(c fiber.Ctx, result map[string]any) error {
	requestKey := c.Query("key")
	if requestKey != "" {
		keys := strings.Split(requestKey, ",")
		if len(keys) == 1 {
			return c.JSON(result[keys[0]])
		}
		data := make(map[string]any)
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

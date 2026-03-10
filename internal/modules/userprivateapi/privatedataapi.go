package userprivateapi

import (
	"crypto/subtle"
	harukiUtils "haruki-suite/utils"
	harukiApiHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/authorizesocialplatforminfo"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	harukiLogger "haruki-suite/utils/logger"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func mapPrivateGameAccountLookupError(err error) *fiber.Error {
	if err == nil {
		return nil
	}
	if postgresql.IsNotFound(err) {
		return fiber.NewError(fiber.StatusNotFound, "game account not found")
	}
	return fiber.NewError(fiber.StatusInternalServerError, "failed to query game account")
}

func mapPrivateAuthorizationLookupError(err error) *fiber.Error {
	if err == nil {
		return nil
	}
	return fiber.NewError(fiber.StatusInternalServerError, "failed to verify authorization")
}

func mapPrivateDataQueryError(err error) *fiber.Error {
	if err == nil {
		return nil
	}
	return fiber.NewError(fiber.StatusInternalServerError, "failed to query user data")
}

func mapPrivateBindingOwnerError(binding *postgresql.GameAccountBinding) *fiber.Error {
	if binding == nil {
		return fiber.NewError(fiber.StatusNotFound, "game account not found")
	}
	if binding.Edges.User == nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to query game account owner")
	}
	if binding.Edges.User.Banned {
		return fiber.NewError(fiber.StatusForbidden, "forbidden: account owner is banned")
	}
	return nil
}

func ValidateUserPermission(apiHelper *harukiApiHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		if apiHelper == nil {
			return harukiApiHelper.ErrorInternal(c, "private api is not configured")
		}
		expectedToken, requiredAgentKeyword := apiHelper.GetPrivateAPIAuth()
		if strings.TrimSpace(expectedToken) == "" {
			harukiLogger.Errorf("private api token is not configured")
			return harukiApiHelper.ErrorInternal(c, "private api is not configured")
		}
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
			return harukiApiHelper.ErrorBadRequest(c, "invalid server")
		}
		dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
		if err != nil {
			return harukiApiHelper.ErrorBadRequest(c, "invalid data_type")
		}
		userID, err := strconv.ParseInt(userIDStr, 10, 64)
		if err != nil {
			return harukiApiHelper.ErrorBadRequest(c, "invalid user_id")
		}
		gameAccountBinding, err := apiHelper.DBManager.DB.GameAccountBinding.Query().
			Where(
				gameaccountbinding.ServerEQ(string(server)),
				gameaccountbinding.GameUserIDEQ(userIDStr),
				gameaccountbinding.VerifiedEQ(true),
			).
			WithUser(func(query *postgresql.UserQuery) {
				query.WithSocialPlatformInfo()
			}).
			Only(ctx)
		if lookupErr := mapPrivateGameAccountLookupError(err); lookupErr != nil {
			if lookupErr.Code == fiber.StatusNotFound {
				return harukiApiHelper.ErrorNotFound(c, lookupErr.Message)
			}
			harukiLogger.Errorf("Failed to query game account binding (server=%s,user_id=%s): %v", server, userIDStr, err)
			return harukiApiHelper.ErrorInternal(c, lookupErr.Message)
		}
		if ownerErr := mapPrivateBindingOwnerError(gameAccountBinding); ownerErr != nil {
			switch ownerErr.Code {
			case fiber.StatusNotFound:
				return harukiApiHelper.ErrorNotFound(c, ownerErr.Message)
			case fiber.StatusForbidden:
				return harukiApiHelper.ErrorForbidden(c, ownerErr.Message)
			default:
				harukiLogger.Errorf("Failed to query game account owner (server=%s,user_id=%s): %s", server, userIDStr, ownerErr.Message)
				return harukiApiHelper.ErrorInternal(c, ownerErr.Message)
			}
		}
		dbUser := gameAccountBinding.Edges.User
		authorized, err := isUserAuthorized(c, apiHelper, dbUser, platform, platformUserID)
		if lookupErr := mapPrivateAuthorizationLookupError(err); lookupErr != nil {
			harukiLogger.Errorf("Failed to verify private api authorization (platform=%s,platform_user_id=%s): %v", platform, platformUserID, err)
			return harukiApiHelper.ErrorInternal(c, lookupErr.Message)
		}
		if !authorized {
			return harukiApiHelper.ErrorForbidden(c, "forbidden: invalid platform or platform_user_id for this user")
		}
		result, err := apiHelper.DBManager.Mongo.GetData(ctx, userID, string(server), dataType)
		if lookupErr := mapPrivateDataQueryError(err); lookupErr != nil {
			harukiLogger.Errorf("Failed to query private user data (server=%s,user_id=%s,data_type=%s): %v", server, userIDStr, dataType, err)
			return harukiApiHelper.ErrorInternal(c, lookupErr.Message)
		}
		if result == nil {
			return harukiApiHelper.ErrorNotFound(c, "user data not found")
		}
		return processRequestKeys(c, result)
	}
}

func isUserAuthorized(c fiber.Ctx, apiHelper *harukiApiHelper.HarukiToolboxRouterHelpers, dbUser *postgresql.User, platform, platformUserID string) (bool, error) {
	if dbUser.Edges.SocialPlatformInfo != nil &&
		dbUser.Edges.SocialPlatformInfo.Platform == platform &&
		dbUser.Edges.SocialPlatformInfo.PlatformUserID == platformUserID {
		return true, nil
	}
	exists, err := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo.Query().
		Where(
			authorizesocialplatforminfo.UserIDEQ(dbUser.ID),
			authorizesocialplatforminfo.PlatformEQ(platform),
			authorizesocialplatforminfo.PlatformUserIDEQ(platformUserID),
		).
		Exist(c.Context())
	if err != nil {
		return false, err
	}
	return exists, nil
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

func RegisterUserPrivateAPIRoutes(apiHelper *harukiApiHelper.HarukiToolboxRouterHelpers) {
	api := apiHelper.Router.Group("/private/:server/:data_type/:user_id", ValidateUserPermission(apiHelper))

	api.Get("/", handleGetPrivateData(apiHelper))
}

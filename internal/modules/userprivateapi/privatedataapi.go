package userprivateapi

import (
	"crypto/subtle"
	harukiUtils "haruki-suite/utils"
	harukiApiHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/authorizesocialplatforminfo"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	"haruki-suite/utils/database/postgresql/socialplatforminfo"
	harukiLogger "haruki-suite/utils/logger"
	"strconv"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v3"
	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/sync/errgroup"
)

func mapPrivateGameAccountLookupError(err error) *fiber.Error {
	if err == nil {
		return nil
	}
	if postgresql.IsNotFound(err) {
		return fiber.NewError(fiber.StatusNotFound, "account binding not found")
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
		return fiber.NewError(fiber.StatusNotFound, "account binding not found")
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
		var (
			mu                 sync.Mutex
			gameAccountBinding *postgresql.GameAccountBinding
			authorized         bool
		)
		g, gCtx := errgroup.WithContext(ctx)
		g.Go(func() error {
			binding, qErr := apiHelper.DBManager.DB.GameAccountBinding.Query().
				Where(
					gameaccountbinding.ServerEQ(string(server)),
					gameaccountbinding.GameUserIDEQ(userIDStr),
					gameaccountbinding.VerifiedEQ(true),
				).
				WithUser(func(query *postgresql.UserQuery) {
					query.WithSocialPlatformInfo()
				}).
				Only(gCtx)
			mu.Lock()
			gameAccountBinding = binding
			mu.Unlock()
			return qErr
		})
		g.Go(func() error {
			exists, qErr := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo.Query().
				Where(
					authorizesocialplatforminfo.PlatformEQ(platform),
					authorizesocialplatforminfo.PlatformUserIDEQ(platformUserID),
				).
				Exist(gCtx)
			mu.Lock()
			authorized = exists
			mu.Unlock()
			return qErr
		})
		if waitErr := g.Wait(); waitErr != nil {
			if gameAccountBinding == nil {
				if lookupErr := mapPrivateGameAccountLookupError(waitErr); lookupErr != nil {
					if lookupErr.Code == fiber.StatusNotFound {
						return harukiApiHelper.ErrorNotFound(c, lookupErr.Message)
					}
					harukiLogger.Errorf("Failed to query game account binding (server=%s,user_id=%s): %v", server, userIDStr, waitErr)
					return harukiApiHelper.ErrorInternal(c, lookupErr.Message)
				}
			}
			harukiLogger.Errorf("Failed to verify private api authorization (platform=%s,platform_user_id=%s): %v", platform, platformUserID, waitErr)
			return harukiApiHelper.ErrorInternal(c, "failed to verify authorization")
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
		if !authorized {
			if dbUser.Edges.SocialPlatformInfo != nil &&
				dbUser.Edges.SocialPlatformInfo.Platform == platform &&
				dbUser.Edges.SocialPlatformInfo.PlatformUserID == platformUserID {
				authorized = true
			}
		}
		if !authorized {
			return harukiApiHelper.ErrorForbidden(c, "forbidden: invalid platform or platform_user_id for this user")
		}
		result, err := apiHelper.DBManager.Mongo.GetData(ctx, userID, string(server), dataType)
		if lookupErr := mapPrivateDataQueryError(err); lookupErr != nil {
			harukiLogger.Errorf("Failed to query private user data (server=%s,user_id=%s,data_type=%s): %v", server, userIDStr, dataType, err)
			return harukiApiHelper.ErrorInternal(c, lookupErr.Message)
		}
		if len(result) == 0 {
			return harukiApiHelper.ErrorNotFound(c, "game data not found")
		}
		return processRequestKeys(c, result)
	}
}

func processRequestKeys(c fiber.Ctx, result bson.D) error {
	requestKey := c.Query("key")
	if requestKey != "" {
		keys := strings.Split(requestKey, ",")
		if len(keys) == 1 {
			return c.JSON(bsonDGet(result, keys[0]))
		}
		filtered := make(bson.D, 0, len(keys))
		for _, k := range keys {
			filtered = append(filtered, bson.E{Key: k, Value: bsonDGet(result, k)})
		}
		return c.JSON(filtered)
	}
	return c.JSON(result)
}

func bsonDGet(d bson.D, key string) any {
	for _, elem := range d {
		if elem.Key == key {
			return elem.Value
		}
	}
	return nil
}

func handleGetGameBindings(apiHelper *harukiApiHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		platform := c.Query("platform")
		platformUserID := c.Query("platform_user_id")
		if platform == "" || platformUserID == "" {
			return harukiApiHelper.ErrorBadRequest(c, "both platform and platform_user_id are required")
		}
		var (
			mu          sync.Mutex
			authEntries []*postgresql.AuthorizeSocialPlatformInfo
			directUser  *postgresql.User
		)
		g, gCtx := errgroup.WithContext(ctx)
		g.Go(func() error {
			entries, err := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo.Query().
				Where(
					authorizesocialplatforminfo.PlatformEQ(platform),
					authorizesocialplatforminfo.PlatformUserIDEQ(platformUserID),
					authorizesocialplatforminfo.AllowFastVerificationEQ(true),
				).
				WithUser(func(query *postgresql.UserQuery) {
					query.WithGameAccountBindings(func(bQuery *postgresql.GameAccountBindingQuery) {
						bQuery.Where(gameaccountbinding.VerifiedEQ(true))
					})
				}).
				All(gCtx)
			if err != nil {
				return err
			}
			mu.Lock()
			authEntries = entries
			mu.Unlock()
			return nil
		})
		g.Go(func() error {
			directInfo, err := apiHelper.DBManager.DB.SocialPlatformInfo.Query().
				Where(
					socialplatforminfo.PlatformEQ(platform),
					socialplatforminfo.PlatformUserIDEQ(platformUserID),
				).
				WithUser(func(query *postgresql.UserQuery) {
					query.WithGameAccountBindings(func(bQuery *postgresql.GameAccountBindingQuery) {
						bQuery.Where(gameaccountbinding.VerifiedEQ(true))
					})
				}).
				Only(gCtx)
			if err == nil && directInfo != nil {
				mu.Lock()
				directUser = directInfo.Edges.User
				mu.Unlock()
			} else if !postgresql.IsNotFound(err) {
				return err
			}
			return nil
		})

		if err := g.Wait(); err != nil {
			harukiLogger.Errorf("Failed to query social platforms concurrently (platform=%s,platform_user_id=%s): %v", platform, platformUserID, err)
			return harukiApiHelper.ErrorInternal(c, "failed to query social platforms")
		}

		if len(authEntries) == 0 && directUser == nil {
			return c.JSON([]any{})
		}
		type bindingEntry struct {
			Server     string `json:"server"`
			GameUserID string `json:"gameUserId"`
		}
		type bindingKey struct {
			Server     string
			GameUserID string
		}
		seen := make(map[bindingKey]struct{})
		var result []bindingEntry
		processUserBindings := func(u *postgresql.User) error {
			if u == nil {
				return nil
			}
			if u.Banned {
				return harukiApiHelper.ErrorForbidden(c, "forbidden: account owner is banned")
			}
			for _, b := range u.Edges.GameAccountBindings {
				key := bindingKey{Server: b.Server, GameUserID: b.GameUserID}
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				result = append(result, bindingEntry{
					Server:     b.Server,
					GameUserID: b.GameUserID,
				})
			}
			return nil
		}

		if directUser != nil {
			if err := processUserBindings(directUser); err != nil {
				return err
			}
		}

		for _, entry := range authEntries {
			if err := processUserBindings(entry.Edges.User); err != nil {
				return err
			}
		}

		if result == nil {
			result = []bindingEntry{}
		}
		return c.JSON(result)
	}
}

func RegisterUserPrivateAPIRoutes(apiHelper *harukiApiHelper.HarukiToolboxRouterHelpers) {
	privateAPI := apiHelper.Router.Group("/api/private", ValidateUserPermission(apiHelper))

	privateAPI.Get("/game-data/:server/:data_type/:user_id", handleGetPrivateData(apiHelper))
	privateAPI.Get("/game-binding", handleGetGameBindings(apiHelper))
}

package userprivateapi

import (
	harukiUtils "haruki-suite/utils"
	harukiApiHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/authorizesocialplatforminfo"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	harukiRedis "haruki-suite/utils/database/redis"
	harukiLogger "haruki-suite/utils/logger"
	"strconv"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v3"
	"golang.org/x/sync/errgroup"
)

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
		requestKey := c.Query("key")
		cacheKey := harukiRedis.BuildGameDataCacheKey("private", string(server), string(dataType), userID, requestKey)
		if cached, found, cErr := apiHelper.DBManager.Redis.GetRawCache(ctx, cacheKey); cErr == nil && found {
			c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
			return c.SendString(cached)
		} else if cErr != nil {
			harukiLogger.Warnf("Failed to read private game data cache: %v", cErr)
		}
		result, err := apiHelper.DBManager.Mongo.GetData(ctx, userID, string(server), dataType)
		if lookupErr := mapPrivateDataQueryError(err); lookupErr != nil {
			harukiLogger.Errorf("Failed to query private user data (server=%s,user_id=%s,data_type=%s): %v", server, userIDStr, dataType, err)
			return harukiApiHelper.ErrorInternal(c, lookupErr.Message)
		}
		if len(result) == 0 {
			return harukiApiHelper.ErrorNotFound(c, "game data not found")
		}
		resp := buildPrivateDataResponse(requestKey, result)
		if encoded, mErr := sonic.Marshal(resp); mErr == nil {
			if cErr := apiHelper.DBManager.Redis.SetRawCache(ctx, cacheKey, string(encoded), 300*time.Second); cErr != nil {
				harukiLogger.Warnf("Failed to write private game data cache: %v", cErr)
			}
		} else {
			harukiLogger.Warnf("Failed to marshal private game data cache: %v", mErr)
		}
		return c.JSON(resp)
	}
}

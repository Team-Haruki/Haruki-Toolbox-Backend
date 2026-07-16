package userprivateapi

import (
	"context"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiApiHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api/data"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/authorizesocialplatforminfo"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountbinding"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	perfdebug "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/perfdebug"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v3"
	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/sync/singleflight"
)

// privateReadTimeout bounds the whole box-read path. Healthy reads are ~60ms, so
// this ceiling only trips under real contention, where failing fast (5xx) is far
// better than parking a DB connection until the client's ~30s timeout fires.
const privateReadTimeout = 3 * time.Second

// privateReadSlowThreshold is the total-read duration above which a per-stage
// autopsy line is logged when profiling is enabled. It sits well above the healthy
// ~60ms so it only fires on genuinely contended reads.
const privateReadSlowThreshold = 500 * time.Millisecond

// privateDataGroup collapses concurrent cache misses for the same cacheKey into a
// single Mongo read + marshal + cache write, so a same-key burst does not stampede
// the database with duplicate full-document pulls.
var privateDataGroup singleflight.Group

func handleGetPrivateData(apiHelper *harukiApiHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		// Bound every downstream DB/cache op so a contended read fails fast instead
		// of parking a connection until the caller's ~30s timeout.
		ctx, cancel := context.WithTimeout(c.Context(), privateReadTimeout)
		defer cancel()
		start := time.Now()
		serverStr := c.Params("server")
		dataTypeStr := c.Params("data_type")
		userIDStr := c.Params("user_id")
		// Slow-read autopsy: when profiling is on, log a per-stage breakdown for reads
		// that cross the threshold, so a spike can be attributed to PG vs Redis vs
		// Mongo without server-side slow logs (client-side pool waits are invisible
		// there). Timestamps are captured unconditionally (~ns) and only formatted when
		// the read is slow and profiling is enabled.
		var dBinding, dAuth, dCache, dLoad time.Duration
		defer func() {
			if !perfdebug.Enabled() {
				return
			}
			total := time.Since(start)
			if total < privateReadSlowThreshold {
				return
			}
			harukiLogger.Warnf("slow private read: server=%s data_type=%s user_id=%s total=%s binding=%s auth=%s cache=%s load=%s",
				serverStr, dataTypeStr, userIDStr, total.Round(time.Millisecond),
				dBinding.Round(time.Millisecond), dAuth.Round(time.Millisecond),
				dCache.Round(time.Millisecond), dLoad.Round(time.Millisecond))
		}()
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
		// Resolve the data owner from the verified binding first; the authorization
		// check below must be scoped to that owner, so the two queries cannot run
		// concurrently.
		bindingStart := time.Now()
		gameAccountBinding, bindingErr := apiHelper.DBManager.DB.GameAccountBinding.Query().
			Where(
				gameaccountbinding.ServerEQ(string(server)),
				gameaccountbinding.GameUserIDEQ(userIDStr),
				gameaccountbinding.VerifiedEQ(true),
			).
			WithUser(func(query *postgresql.UserQuery) {
				query.WithSocialPlatformInfo()
			}).
			Only(ctx)
		dBinding = time.Since(bindingStart)
		if bindingErr != nil {
			if lookupErr := mapPrivateGameAccountLookupError(bindingErr); lookupErr != nil {
				if lookupErr.Code == fiber.StatusNotFound {
					return harukiApiHelper.ErrorNotFound(c, lookupErr.Message)
				}
				harukiLogger.Errorf("Failed to query game account binding (server=%s,user_id=%s): %v", server, userIDStr, bindingErr)
				return harukiApiHelper.ErrorInternal(c, lookupErr.Message)
			}
			harukiLogger.Errorf("Failed to query game account binding (server=%s,user_id=%s): %v", server, userIDStr, bindingErr)
			return harukiApiHelper.ErrorInternal(c, "failed to query game account binding")
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

		// The requester is authorized if either: they are the owner's own bound
		// social account, or the owner authorized this platform account (any
		// authorize-social grant — the direct data fetch intentionally does NOT
		// require allow_fast_verification; that flag gates the bindings query API
		// only). Both must be scoped to the resolved owner (dbUser.ID); querying
		// the authorize table without the owner constraint is a cross-user IDOR.
		authorized := dbUser.Edges.SocialPlatformInfo != nil &&
			dbUser.Edges.SocialPlatformInfo.Platform == platform &&
			dbUser.Edges.SocialPlatformInfo.PlatformUserID == platformUserID
		if !authorized {
			authStart := time.Now()
			exists, authErr := apiHelper.DBManager.DB.AuthorizeSocialPlatformInfo.Query().
				Where(
					authorizesocialplatforminfo.UserIDEQ(dbUser.ID),
					authorizesocialplatforminfo.PlatformEQ(platform),
					authorizesocialplatforminfo.PlatformUserIDEQ(platformUserID),
				).
				Exist(ctx)
			dAuth = time.Since(authStart)
			if authErr != nil {
				harukiLogger.Errorf("Failed to verify private api authorization (platform=%s,platform_user_id=%s): %v", platform, platformUserID, authErr)
				return harukiApiHelper.ErrorInternal(c, "failed to verify authorization")
			}
			authorized = exists
		}
		if !authorized {
			return harukiApiHelper.ErrorForbidden(c, "forbidden: invalid platform or platform_user_id for this user")
		}
		requestKey := c.Query("key")
		if data.CheckNotModified(ctx, c, apiHelper, userID, server, dataType, requestKey, false) {
			return c.SendStatus(fiber.StatusNotModified)
		}
		cacheKey := harukiRedis.BuildGameDataCacheKey("private", string(server), string(dataType), userID, requestKey)
		cacheStart := time.Now()
		cached, cacheFound, cErr := apiHelper.DBManager.Redis.GetRawCache(ctx, cacheKey)
		dCache = time.Since(cacheStart)
		if cErr == nil && cacheFound {
			c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
			return c.SendString(cached)
		} else if cErr != nil {
			harukiLogger.Warnf("Failed to read private game data cache: %v", cErr)
		}
		loadStart := time.Now()
		body, found, err := loadPrivateData(apiHelper, cacheKey, server, dataType, userID, requestKey)
		dLoad = time.Since(loadStart)
		if lookupErr := mapPrivateDataQueryError(err); lookupErr != nil {
			harukiLogger.Errorf("Failed to query private user data (server=%s,user_id=%s,data_type=%s): %v", server, userIDStr, dataType, err)
			return harukiApiHelper.ErrorInternal(c, lookupErr.Message)
		}
		if !found {
			return harukiApiHelper.ErrorNotFound(c, "game data not found")
		}
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
		return c.SendString(body)
	}
}

// loadPrivateData resolves the JSON body for cacheKey, collapsing concurrent
// same-key misses via singleflight so a burst does a single Mongo pull + marshal +
// cache write. It reports whether the document exists and marshals the response
// exactly once (the previous code marshaled once for the cache and again in c.JSON).
func loadPrivateData(
	apiHelper *harukiApiHelper.HarukiToolboxRouterHelpers,
	cacheKey string,
	server harukiUtils.SupportedDataUploadServer,
	dataType harukiUtils.UploadDataType,
	userID int64,
	requestKey string,
) (string, bool, error) {
	type payload struct {
		body  string
		found bool
	}
	v, err, _ := privateDataGroup.Do(cacheKey, func() (any, error) {
		// Detach from any single caller's request lifetime: this fetch serves every
		// coalesced waiter, so a leader disconnecting must not fail the others. It is
		// still bounded so it cannot run away.
		fetchCtx, cancel := context.WithTimeout(context.Background(), privateReadTimeout)
		defer cancel()
		result, fetchErr := fetchPrivateData(fetchCtx, apiHelper, server, dataType, userID, requestKey)
		if fetchErr != nil {
			return nil, fetchErr
		}
		if len(result) == 0 {
			return payload{found: false}, nil
		}
		encoded, encErr := sonic.Marshal(buildPrivateDataResponse(requestKey, result))
		if encErr != nil {
			return nil, encErr
		}
		// Detach the cache write from the request deadline so a near-deadline but
		// successful read still populates the cache for subsequent requests.
		cacheCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if cErr := apiHelper.DBManager.Redis.SetRawCache(cacheCtx, cacheKey, string(encoded), 300*time.Second); cErr != nil {
			harukiLogger.Warnf("Failed to write private game data cache: %v", cErr)
		}
		return payload{body: string(encoded), found: true}, nil
	})
	if err != nil {
		return "", false, err
	}
	p := v.(payload)
	return p.body, p.found, nil
}

// fetchPrivateData reads the stored document, projecting to only the requested
// keys when a comma-separated `key` filter is supplied so the box read no longer
// transfers and decodes the full multi-MB document for a keyed request.
func fetchPrivateData(
	ctx context.Context,
	apiHelper *harukiApiHelper.HarukiToolboxRouterHelpers,
	server harukiUtils.SupportedDataUploadServer,
	dataType harukiUtils.UploadDataType,
	userID int64,
	requestKey string,
) (bson.D, error) {
	if projection := buildKeyProjection(requestKey); projection != nil {
		return apiHelper.DBManager.Mongo.GetDataWithProjection(ctx, userID, string(server), dataType, projection)
	}
	return apiHelper.DBManager.Mongo.GetData(ctx, userID, string(server), dataType)
}

// buildKeyProjection returns an inclusion projection limited to the requested keys,
// or nil when no explicit key was requested — in which case the caller fetches the
// full document, matching legacy behavior.
//
// We intentionally do NOT exclude _id. Excluding it would make Mongo return an empty
// document when a request asks only for keys the document does not contain, which the
// len()==0 check would then misreport as "not found" (404) even though the account
// exists. Keeping the implicit _id inclusion guarantees an existing document always
// projects to a non-empty result; _id never appears in the response because
// buildPrivateDataResponse only emits the explicitly requested keys.
func buildKeyProjection(requestKey string) bson.M {
	if requestKey == "" {
		return nil
	}
	projection := bson.M{}
	for _, k := range strings.Split(requestKey, ",") {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		projection[k] = 1
	}
	if len(projection) == 0 {
		return nil
	}
	return projection
}

package oauth2

import (
	"context"
	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api/data"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/oauth2"
	"strconv"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v3"
	"golang.org/x/sync/singleflight"
)

// oauth2GameDataReadTimeout bounds the whole OAuth2 game-data read (PG access
// check, Redis cache ops, Mongo fetch) — Fiber v3 request contexts carry no
// deadline, so this mirrors the private box-read fix.
const oauth2GameDataReadTimeout = 3 * time.Second

// oauth2GameDataGroup collapses concurrent cache misses for the same cacheKey
// into a single Mongo read + marshal + cache write (see publicDataGroup for why
// misses on these surfaces are correlated).
var oauth2GameDataGroup singleflight.Group

func handleOAuth2GetGameData(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		// Bound every downstream DB/cache op so a contended read fails fast
		// instead of parking a connection — mirrors the private box-read path.
		ctx, cancel := context.WithTimeout(c.Context(), oauth2GameDataReadTimeout)
		defer cancel()

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
		// Resolve the document generation once: it drives both the conditional
		// 304 answer and which versioned cache entry this request may read.
		stamp, stampConfirmed, stampErr := data.ResolveGameDataStamp(ctx, apiHelper, server, dataType, gameUserID)
		if stampErr != nil {
			harukiLogger.Warnf("Failed to resolve OAuth2 game data stamp (server=%s,user_id=%d): %v", server, gameUserID, stampErr)
			stamp = -1 // unresolved: bypass the cache, never 304
		}
		// Fetched once per request: resolving the allowlist costs a Redis round
		// trip, and both the conditional gate and the read-key digest need it.
		var suiteAllowedKeys []string
		if dataType == harukiUtils.UploadDataTypeSuite {
			suiteAllowedKeys = apiHelper.GetPublicAPIAllowedKeys()
		}
		if stampConfirmed && data.CheckNotModified(c, dataType, requestKey, true, suiteAllowedKeys, stamp) {
			return c.SendStatus(fiber.StatusNotModified)
		}
		var cacheKey string
		if stamp > 0 {
			cacheKey = harukiRedis.BuildVersionedGameDataCacheKey("oauth2", string(server), string(dataType), gameUserID, requestKey, stamp)
			// Suite bodies are shaped by the public key allowlist, so entries
			// are keyed by it too — an allowlist edit moves readers to fresh
			// entries even when the stamp is unchanged. The write side appends
			// its own digest inside the flight, from the very slice that shaped
			// the body, so key and body stay atomic under concurrent edits.
			readKey := cacheKey
			if dataType == harukiUtils.UploadDataTypeSuite {
				readKey += ":a=" + data.PublicAllowlistDigest(suiteAllowedKeys)
			}
			if cached, found, cErr := apiHelper.DBManager.Redis.GetRawCache(ctx, readKey); cErr == nil && found {
				if sErr := data.ServeGameDataBody(c, cached); sErr == nil {
					return nil
				} else {
					harukiLogger.Warnf("Failed to serve cached OAuth2 game data, refetching: %v", sErr)
				}
			} else if cErr != nil {
				harukiLogger.Warnf("Failed to read OAuth2 game data cache: %v", cErr)
			}
		}

		body, err := loadOAuth2GameData(apiHelper, cacheKey, server, dataType, gameUserID, requestKey, stamp)
		if err != nil {
			if fErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fErr.Code, fErr.Message, nil)
			}
			harukiLogger.Errorf("Failed to load OAuth2 game data: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to get user data")
		}
		if sErr := data.ServeGameDataBody(c, body); sErr != nil {
			harukiLogger.Errorf("Failed to serve OAuth2 game data: %v", sErr)
			return harukiAPIHelper.ErrorInternal(c, "failed to get user data")
		}
		return nil
	}
}

// loadOAuth2GameData resolves the stored (gzip-compressed) body for this
// request, collapsing concurrent same-request misses via singleflight and
// marshaling+compressing the response exactly once — the same shape as
// loadPublicGameData/loadPrivateData. The access check stays with the
// per-request caller; only the post-authz materialization is shared. cacheKey
// may be empty (unresolved document generation), in which case the result is
// served but not cached.
func loadOAuth2GameData(
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	cacheKey string,
	server harukiUtils.SupportedDataUploadServer,
	dataType harukiUtils.UploadDataType,
	gameUserID int64,
	requestKey string,
	stamp int64,
) (string, error) {
	// Generation-independent flight key on the raw request key (see
	// loadPublicGameData): only byte-identical requests may share a result.
	flightKey := string(server) + ":" + string(dataType) + ":" + strconv.FormatInt(gameUserID, 10) + "\x00" + requestKey
	v, err, _ := oauth2GameDataGroup.Do(flightKey, func() (any, error) {
		// Detached from any single caller's request lifetime; still bounded.
		fetchCtx, cancel := context.WithTimeout(context.Background(), oauth2GameDataReadTimeout)
		defer cancel()
		publicAPIAllowedKeys := apiHelper.GetPublicAPIAllowedKeys()
		allowedKeySet := make(map[string]struct{}, len(publicAPIAllowedKeys))
		for _, k := range publicAPIAllowedKeys {
			allowedKeySet[k] = struct{}{}
		}
		var resp any
		var loadErr error
		if dataType == harukiUtils.UploadDataTypeSuite {
			resp, loadErr = data.HandleSuiteRequest(fetchCtx, apiHelper, gameUserID, server, requestKey, allowedKeySet, publicAPIAllowedKeys)
		} else {
			resp, loadErr = data.HandleMysekaiRequest(fetchCtx, apiHelper, gameUserID, server, requestKey)
		}
		if loadErr != nil {
			return nil, loadErr
		}
		encoded, mErr := sonic.Marshal(resp)
		if mErr != nil {
			return nil, mErr
		}
		body, cmpErr := data.CompressGameDataBody(encoded)
		if cmpErr != nil {
			harukiLogger.Warnf("Failed to compress OAuth2 game data body, storing plain: %v", cmpErr)
			body = string(encoded)
		}
		if cacheKey != "" {
			writeKey := cacheKey
			if dataType == harukiUtils.UploadDataTypeSuite {
				// Digest from the same slice that shaped this body, so an
				// allowlist edit mid-flight cannot pair a new-config key with
				// an old-config body (or vice versa).
				writeKey += ":a=" + data.PublicAllowlistDigest(publicAPIAllowedKeys)
			}
			// Detach the cache write from the read deadline so a near-deadline
			// but successful read still populates the cache for subsequent
			// requests. The write is fenced (see ConfirmGameDataCacheWrite) so
			// a leader racing an upload or a cache wipe cannot pin a stale body
			// under a live generation key.
			cacheCtx, cancelCache := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancelCache()
			if data.ConfirmGameDataCacheWrite(cacheCtx, apiHelper, server, dataType, gameUserID, stamp) {
				if cErr := apiHelper.DBManager.Redis.SetRawCache(cacheCtx, writeKey, body, data.GameDataCacheWriteTTL(requestKey, stamp)); cErr != nil {
					harukiLogger.Warnf("Failed to write OAuth2 game data cache: %v", cErr)
				}
			}
		}
		return body, nil
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

func registerOAuth2GameDataRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig) {
	o := apiHelper.Router.Group("/api/oauth2/game-data")
	o.Get("/:server/:data_type/:user_id",
		harukiOAuth2.VerifyOAuth2Token(hydraConfig, apiHelper.DBManager.DB, harukiOAuth2.ScopeGameDataRead, checkHydraOAuth2ClientActive(hydraConfig)),
		handleOAuth2GetGameData(apiHelper),
	)
}

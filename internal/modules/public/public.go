package public

import (
	"context"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountbinding"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"strconv"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api/data"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v3"
	"golang.org/x/sync/singleflight"
)

// publicReadTimeout bounds the whole public read path (PG binding lookup, Redis
// cache ops, Mongo fetch). Fiber v3 request contexts carry no deadline, so
// without this a stalled dependency parks the request indefinitely — the same
// landmine class fixed for private box reads in v8.3.0.
const publicReadTimeout = 3 * time.Second

// publicDataGroup collapses concurrent cache misses for the same cacheKey into a
// single Mongo read + marshal + cache write. Misses here are correlated: every
// upload invalidates all cached surfaces for that user and then fans out webhook
// notifications, so subscribers commonly re-fetch the same key at once.
var publicDataGroup singleflight.Group

func validatePublicAPIAccess(record *postgresql.GameAccountBinding, dataType harukiUtils.UploadDataType) bool {
	if record == nil || !record.Verified {
		return false
	}
	if record.Edges.User == nil || record.Edges.User.Banned {
		return false
	}
	if dataType == harukiUtils.UploadDataTypeSuite {
		return record.Suite != nil && record.Suite.AllowPublicApi
	}
	if dataType == harukiUtils.UploadDataTypeMysekai {
		return record.Mysekai != nil && record.Mysekai.AllowPublicApi
	}
	return false
}

func handlePublicDataRequest(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		// Bound every downstream DB/cache op so a contended read fails fast
		// instead of parking a connection — mirrors the private box-read path.
		ctx, cancel := context.WithTimeout(c.Context(), publicReadTimeout)
		defer cancel()
		server, dataType, userID, userIDStr, parseErr := parseParams(c)
		if parseErr != nil {
			return harukiAPIHelper.ErrorNotFound(c, "not found")
		}

		record, err := fetchAccountBinding(ctx, apiHelper, server, userIDStr)
		if err != nil {
			if !postgresql.IsNotFound(err) {
				harukiLogger.Errorf("Failed to query account binding: %v", err)
				return harukiAPIHelper.ErrorInternal(c, "failed to query account binding")
			}
			// Identical body to the not-accessible case below so a caller cannot
			// distinguish "no such binding" from "binding exists but not public".
			return harukiAPIHelper.ErrorNotFound(c, "not found")
		}
		if !validatePublicAPIAccess(record, dataType) {
			return harukiAPIHelper.ErrorNotFound(c, "not found")
		}

		requestKey := c.Query("key")
		// Resolve the document generation once: it drives both the conditional
		// 304 answer and which versioned cache entry this request may read.
		stamp, stampConfirmed, stampErr := data.ResolveGameDataStamp(ctx, apiHelper, server, dataType, userID)
		if stampErr != nil {
			harukiLogger.Warnf("Failed to resolve public game data stamp (server=%s,user_id=%d): %v", server, userID, stampErr)
			stamp = -1 // unresolved: bypass the cache, never 304
		}
		if stampConfirmed && data.CheckNotModified(c, apiHelper, dataType, requestKey, true, stamp) {
			return c.SendStatus(fiber.StatusNotModified)
		}
		var cacheKey string
		if stamp > 0 {
			cacheKey = harukiRedis.BuildVersionedGameDataCacheKey("public", string(server), string(dataType), userID, requestKey, stamp)
			// Suite bodies are shaped by the public key allowlist, so entries
			// are keyed by it too — an allowlist edit moves readers to fresh
			// entries even when the stamp is unchanged. The write side appends
			// its own digest inside the flight, from the very slice that shaped
			// the body, so key and body stay atomic under concurrent edits.
			readKey := cacheKey
			if dataType == harukiUtils.UploadDataTypeSuite {
				readKey += ":a=" + data.PublicAllowlistDigest(apiHelper.GetPublicAPIAllowedKeys())
			}
			if cached, found, err := apiHelper.DBManager.Redis.GetRawCache(ctx, readKey); err == nil && found {
				if sErr := data.ServeGameDataBody(c, cached); sErr == nil {
					return nil
				} else {
					harukiLogger.Warnf("Failed to serve cached public game data, refetching: %v", sErr)
				}
			} else if err != nil {
				harukiLogger.Warnf("Failed to read public game data cache: %v", err)
			}
		}
		body, err := loadPublicGameData(apiHelper, cacheKey, server, dataType, userID, requestKey, stamp)
		if err != nil {
			if fErr, ok := err.(*fiber.Error); ok {
				if fErr.Code == fiber.StatusInternalServerError {
					return harukiAPIHelper.ErrorInternal(c, "failed to get user data")
				}
				return harukiAPIHelper.ErrorNotFound(c, "not found")
			}
			harukiLogger.Errorf("Failed to load public game data: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to get user data")
		}
		if sErr := data.ServeGameDataBody(c, body); sErr != nil {
			harukiLogger.Errorf("Failed to serve public game data: %v", sErr)
			return harukiAPIHelper.ErrorInternal(c, "failed to get user data")
		}
		return nil
	}
}

// loadPublicGameData resolves the stored (gzip-compressed) body for this
// request, collapsing concurrent same-request misses via singleflight and
// marshaling+compressing the response exactly once — the same shape as
// loadPrivateData on the private box-read path. Authorization stays with the
// per-request caller; only the post-authz data materialization is shared.
// cacheKey may be empty (unresolved document generation), in which case the
// result is served but not cached.
func loadPublicGameData(
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	cacheKey string,
	server harukiUtils.SupportedDataUploadServer,
	dataType harukiUtils.UploadDataType,
	userID int64,
	requestKey string,
	stamp int64,
) (string, error) {
	// The flight key is generation-independent (coalesce all concurrent misses
	// for one document+filter) and uses the raw request key: only byte-identical
	// requests may share a result, since a padded key fails validation while its
	// trimmed twin succeeds.
	flightKey := string(server) + ":" + string(dataType) + ":" + strconv.FormatInt(userID, 10) + "\x00" + requestKey
	v, err, _ := publicDataGroup.Do(flightKey, func() (any, error) {
		// Detached from any single caller's request lifetime: this fetch serves
		// every coalesced waiter, so a leader disconnecting must not fail the
		// others. Still bounded so it cannot run away.
		fetchCtx, cancel := context.WithTimeout(context.Background(), publicReadTimeout)
		defer cancel()
		publicAPIAllowedKeys := apiHelper.GetPublicAPIAllowedKeys()
		allowedKeySet := make(map[string]struct{}, len(publicAPIAllowedKeys))
		for _, k := range publicAPIAllowedKeys {
			allowedKeySet[k] = struct{}{}
		}
		var resp any
		var loadErr error
		if dataType == harukiUtils.UploadDataTypeSuite {
			resp, loadErr = data.HandleSuiteRequest(fetchCtx, apiHelper, userID, server, requestKey, allowedKeySet, publicAPIAllowedKeys)
		} else {
			resp, loadErr = data.HandleMysekaiRequest(fetchCtx, apiHelper, userID, server, requestKey)
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
			harukiLogger.Warnf("Failed to compress public game data body, storing plain: %v", cmpErr)
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
			if data.ConfirmGameDataCacheWrite(cacheCtx, apiHelper, server, dataType, userID, stamp) {
				if cErr := apiHelper.DBManager.Redis.SetRawCache(cacheCtx, writeKey, body, data.GameDataCacheWriteTTL(requestKey, stamp)); cErr != nil {
					harukiLogger.Warnf("Failed to write public game data cache: %v", cErr)
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

func parseParams(c fiber.Ctx) (harukiUtils.SupportedDataUploadServer, harukiUtils.UploadDataType, int64, string, *fiber.Error) {
	serverStr := c.Params("server")
	server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
	if err != nil {
		return "", "", 0, "", fiber.NewError(fiber.StatusBadRequest, "invalid server")
	}
	dataTypeStr := c.Params("data_type")
	dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
	if err != nil {
		return "", "", 0, "", fiber.NewError(fiber.StatusBadRequest, "invalid data_type")
	}
	userIDStr := c.Params("user_id")
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		return "", "", 0, "", fiber.NewError(fiber.StatusBadRequest, "invalid user_id")
	}
	return server, dataType, userID, userIDStr, nil
}

func fetchAccountBinding(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, server harukiUtils.SupportedDataUploadServer, userIDStr string) (*postgresql.GameAccountBinding, error) {
	return apiHelper.DBManager.DB.GameAccountBinding.
		Query().
		Where(
			gameaccountbinding.ServerEQ(string(server)),
			gameaccountbinding.GameUserIDEQ(userIDStr),
		).
		WithUser().
		Only(ctx)
}

func RegisterPublicRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	for _, prefix := range []string{"/public/:server/:data_type", "/api/public/:server/:data_type"} {
		group := apiHelper.Router.Group(prefix)
		group.Get("/:user_id", handlePublicDataRequest(apiHelper))
	}
}

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
		if data.CheckNotModified(ctx, c, apiHelper, gameUserID, server, dataType, requestKey, true) {
			return c.SendStatus(fiber.StatusNotModified)
		}
		cacheKey := harukiRedis.BuildGameDataCacheKey("oauth2", string(server), string(dataType), gameUserID, requestKey)
		if cached, found, cErr := apiHelper.DBManager.Redis.GetRawCache(ctx, cacheKey); cErr == nil && found {
			c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
			return c.SendString(cached)
		} else if cErr != nil {
			harukiLogger.Warnf("Failed to read OAuth2 game data cache: %v", cErr)
		}

		body, err := loadOAuth2GameData(apiHelper, cacheKey, server, dataType, gameUserID, requestKey)
		if err != nil {
			if fErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fErr.Code, fErr.Message, nil)
			}
			harukiLogger.Errorf("Failed to load OAuth2 game data: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to get user data")
		}
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
		return c.SendString(body)
	}
}

// loadOAuth2GameData resolves the JSON body for cacheKey, collapsing concurrent
// same-key misses via singleflight and marshaling the response exactly once —
// the same shape as loadPublicGameData/loadPrivateData. The access check stays
// with the per-request caller; only the post-authz materialization is shared.
func loadOAuth2GameData(
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	cacheKey string,
	server harukiUtils.SupportedDataUploadServer,
	dataType harukiUtils.UploadDataType,
	gameUserID int64,
	requestKey string,
) (string, error) {
	// Key the flight on cacheKey plus the raw request key (see loadPublicGameData):
	// only byte-identical requests may share a result; the cache itself stays
	// keyed by the trimmed cacheKey.
	flightKey := cacheKey + "\x00" + requestKey
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
		body := string(encoded)
		// Detach the cache write from the read deadline so a near-deadline but
		// successful read still populates the cache for subsequent requests.
		cacheCtx, cancelCache := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelCache()
		if cErr := apiHelper.DBManager.Redis.SetRawCache(cacheCtx, cacheKey, body, 300*time.Second); cErr != nil {
			harukiLogger.Warnf("Failed to write OAuth2 game data cache: %v", cErr)
		}
		return body, nil
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

func registerOAuth2GameDataRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	o := apiHelper.Router.Group("/api/oauth2/game-data")
	o.Get("/:server/:data_type/:user_id",
		harukiOAuth2.VerifyOAuth2Token(apiHelper.DBManager.DB, harukiOAuth2.ScopeGameDataRead),
		handleOAuth2GetGameData(apiHelper),
	)
}

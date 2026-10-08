package usergamebindings

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api/data"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/circuitbreaker"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/sekaiapi"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
	"golang.org/x/sync/singleflight"
)

// ownedGameAccountReadTimeout bounds the owned-account data read (PG access
// check + game-data PostgreSQL fetch); Fiber v3 request contexts carry no
// deadline.
const ownedGameAccountReadTimeout = 3 * time.Second

type ownedGameAccountDataType string

const (
	ownedGameAccountDataTypeSuite   ownedGameAccountDataType = "suite"
	ownedGameAccountDataTypeMysekai ownedGameAccountDataType = "mysekai"
	ownedGameAccountDataTypeProfile ownedGameAccountDataType = "profile"
)

func parseOwnedGameAccountDataType(raw string) (ownedGameAccountDataType, *fiber.Error) {
	dataType := ownedGameAccountDataType(strings.ToLower(strings.TrimSpace(raw)))
	switch dataType {
	case ownedGameAccountDataTypeSuite, ownedGameAccountDataTypeMysekai, ownedGameAccountDataTypeProfile:
		return dataType, nil
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid data_type")
	}
}

func buildAllowedKeySet(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) (map[string]struct{}, []string) {
	allowedKeys := apiHelper.GetAllowedKeys()
	allowedKeySet := make(map[string]struct{}, len(allowedKeys))
	for _, key := range allowedKeys {
		allowedKeySet[key] = struct{}{}
	}
	return allowedKeySet, allowedKeys
}

func handleGetOwnedGameAccountData(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		// Bound the PG access check and game-data reads; the profile branch sets its
		// own per-server deadline for the upstream game-API call.
		ctx, cancel := context.WithTimeout(c.Context(), ownedGameAccountReadTimeout)
		defer cancel()

		authUserID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}

		serverStr := c.Params("server")
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid server")
		}

		gameUserIDStr := strings.TrimSpace(c.Params("game_user_id"))
		gameUserID, err := strconv.ParseInt(gameUserIDStr, 10, 64)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "game_user_id must be numeric")
		}

		dataType, parseErr := parseOwnedGameAccountDataType(c.Params("data_type"))
		if parseErr != nil {
			return harukiAPIHelper.ErrorBadRequest(c, parseErr.Message)
		}

		access, err := apiHelper.DBManager.DB.CanAccessGameAccountData(ctx, authUserID, string(server), gameUserIDStr, string(dataType), time.Now().UTC())
		if err != nil {
			harukiLogger.Errorf("Failed to verify game account data access: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to verify game account data access")
		}
		if access == nil || !access.Allowed {
			if access == nil || access.OwnerUserID == "" {
				return harukiAPIHelper.ErrorNotFound(c, "binding not found")
			}
			return harukiAPIHelper.ErrorForbidden(c, "not authorized to access this binding")
		}

		requestKey := c.Query("key")
		switch dataType {
		case ownedGameAccountDataTypeSuite:
			allowedKeySet, allowedKeys := buildAllowedKeySet(apiHelper)
			if ownedGameAccountNotModified(ctx, c, apiHelper, gameUserID, server, harukiUtils.UploadDataTypeSuite, requestKey, true, allowedKeys) {
				return c.SendStatus(fiber.StatusNotModified)
			}
			resp, err := data.HandleSuiteRequest(ctx, apiHelper, gameUserID, server, requestKey, allowedKeySet, allowedKeys)
			if err != nil {
				return respondVerifiedGameAccountDataError(c, err)
			}
			return data.SendGameDataResponse(c, resp)
		case ownedGameAccountDataTypeMysekai:
			if ownedGameAccountNotModified(ctx, c, apiHelper, gameUserID, server, harukiUtils.UploadDataTypeMysekai, requestKey, false, nil) {
				return c.SendStatus(fiber.StatusNotModified)
			}
			resp, err := data.HandleMysekaiRequest(ctx, apiHelper, gameUserID, server, requestKey)
			if err != nil {
				return respondVerifiedGameAccountDataError(c, err)
			}
			return data.SendGameDataResponse(c, resp)
		case ownedGameAccountDataTypeProfile:
			// Reached only after CanAccessGameAccountData allowed it, which for a
			// grantee means the owner granted this data type explicitly.
			return sendOwnedGameAccountProfile(c, apiHelper, gameUserIDStr, server)
		default:
			return harukiAPIHelper.ErrorBadRequest(c, "invalid data_type")
		}
	}
}

func ownedGameAccountNotModified(
	ctx context.Context,
	c fiber.Ctx,
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	gameUserID int64,
	server harukiUtils.SupportedDataUploadServer,
	dataType harukiUtils.UploadDataType,
	requestKey string,
	publicKeyFiltered bool,
	publicAllowedKeys []string,
) bool {
	if data.ParseKnownUploadTime(c.Query(data.QueryKnownUploadTime)) <= 0 {
		return false
	}
	stamp, confirmed, err := data.ResolveGameDataStamp(ctx, apiHelper, server, dataType, gameUserID)
	if err != nil {
		harukiLogger.Warnf("Failed to resolve owned game account data stamp (server=%s,data_type=%s,user_id=%d): %v", server, dataType, gameUserID, err)
		return false
	}
	return confirmed && data.CheckNotModified(c, dataType, requestKey, publicKeyFiltered, publicAllowedKeys, stamp)
}

// profileCacheIOTimeout bounds the Redis read and write around a profile view;
// a slow cache falls through to the live call.
const profileCacheIOTimeout = time.Second

// profileViewGroup collapses concurrent views of one game account's profile into
// a single Sekai API call. Callers join only after their own access check, and
// the profile is the same for every authorized viewer.
var profileViewGroup singleflight.Group

type profileViewResult struct {
	result *sekaiapi.HarukiSekaiAPIResult
	body   []byte
	err    error
}

func (r profileViewResult) cacheable() bool {
	return r.err == nil && r.result != nil && r.result.ServerAvailable && r.result.AccountExists && r.result.Body && len(r.body) > 0
}

// sendOwnedGameAccountProfile must only run after CanAccessGameAccountData has
// allowed the caller: the cache below is shared by every authorized viewer of
// the account and holds successful responses only.
func sendOwnedGameAccountProfile(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, gameUserIDStr string, server harukiUtils.SupportedDataUploadServer) error {
	if apiHelper == nil || apiHelper.SekaiAPIClient == nil {
		return harukiAPIHelper.ErrorInternal(c, "profile service unavailable")
	}
	client := apiHelper.SekaiAPIClient
	cacheKey := harukiRedis.BuildSekaiAPIProfileCacheKey(string(server), gameUserIDStr)
	cache, cacheTTL := profileCache(apiHelper)
	if cache != nil {
		readCtx, cancel := context.WithTimeout(c.Context(), profileCacheIOTimeout)
		cached, found, err := cache.GetRawCacheBytes(readCtx, cacheKey)
		cancel()
		if err == nil && found && len(cached) > 0 {
			c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
			return c.Send(cached)
		}
	}

	v, _, _ := profileViewGroup.Do(cacheKey, func() (any, error) {
		// Detached from the leader's request: the result serves every joined caller.
		ctx, cancel := context.WithTimeout(context.Background(), client.ProfileViewTimeout(string(server)))
		defer cancel()
		resultInfo, body, err := client.GetUserProfile(ctx, gameUserIDStr, string(server))
		view := profileViewResult{result: resultInfo, body: body, err: err}
		if cache != nil && view.cacheable() {
			writeCtx, cancelWrite := context.WithTimeout(context.Background(), profileCacheIOTimeout)
			defer cancelWrite()
			if wErr := cache.SetRawCacheBytes(writeCtx, cacheKey, body, cacheTTL); wErr != nil {
				harukiLogger.Warnf("Failed to cache game account profile: %v", wErr)
			}
		}
		return view, nil
	})
	view := v.(profileViewResult)
	return respondOwnedGameAccountProfile(c, view.result, view.body, view.err)
}

// profileCache returns the Redis cache for profile views, or nil when it is
// disabled or Redis is not configured.
func profileCache(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) (*harukiRedis.HarukiRedisManager, time.Duration) {
	ttl := apiHelper.SekaiAPIClient.ProfileCacheTTL()
	if ttl <= 0 || apiHelper.DBManager == nil || apiHelper.DBManager.Redis == nil || apiHelper.DBManager.Redis.Redis == nil {
		return nil, 0
	}
	return apiHelper.DBManager.Redis, ttl
}

func respondOwnedGameAccountProfile(c fiber.Ctx, resultInfo *sekaiapi.HarukiSekaiAPIResult, body []byte, err error) error {
	var circuitOpen *sekaiapi.CircuitOpenError
	if errors.As(err, &circuitOpen) {
		c.Set(fiber.HeaderRetryAfter, strconv.Itoa(circuitbreaker.RetryAfterSeconds(circuitOpen.RetryAfter)))
	}
	if err != nil {
		if resultInfo != nil {
			if !resultInfo.ServerAvailable {
				return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusBadGateway, "game server unavailable", nil)
			}
			if !resultInfo.AccountExists {
				return harukiAPIHelper.ErrorNotFound(c, "game account not found")
			}
		}
		harukiLogger.Errorf("Failed to query game account profile: %v", err)
		return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusBadGateway, "failed to query game account profile", nil)
	}
	if resultInfo == nil {
		harukiLogger.Errorf("Sekai API profile response missing result info")
		return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusBadGateway, "failed to query game account profile", nil)
	}
	if !resultInfo.ServerAvailable {
		return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusBadGateway, "game server unavailable", nil)
	}
	if !resultInfo.AccountExists {
		return harukiAPIHelper.ErrorNotFound(c, "game account not found")
	}
	if !resultInfo.Body || len(body) == 0 {
		return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusBadGateway, "empty game account profile response", nil)
	}

	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	return c.Send(body)
}

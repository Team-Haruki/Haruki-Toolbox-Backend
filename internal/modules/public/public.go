package public

import (
	"context"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	harukiRedis "haruki-suite/utils/database/redis"
	harukiLogger "haruki-suite/utils/logger"
	"strconv"
	"time"

	"haruki-suite/utils/api/data"

	"github.com/gofiber/fiber/v3"
)

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
		ctx := c.Context()
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
			return harukiAPIHelper.ErrorNotFound(c, "account binding not found")
		}
		if !validatePublicAPIAccess(record, dataType) {
			return harukiAPIHelper.ErrorNotFound(c, "not found")
		}

		var resp any
		if dataType != harukiUtils.UploadDataTypeMysekai {
			cacheKey := harukiRedis.CacheKeyBuilderWithAllowedQuery(c, "public_access", "key")
			if found, err := apiHelper.DBManager.Redis.GetCache(ctx, cacheKey, &resp); err == nil && found {
				return c.JSON(resp)
			}
		}
		publicAPIAllowedKeys := apiHelper.GetPublicAPIAllowedKeys()
		allowedKeySet := make(map[string]struct{}, len(publicAPIAllowedKeys))
		for _, k := range publicAPIAllowedKeys {
			allowedKeySet[k] = struct{}{}
		}
		requestKey := c.Query("key")
		if dataType == harukiUtils.UploadDataTypeSuite {
			resp, err = data.HandleSuiteRequest(c, apiHelper, userID, server, requestKey, allowedKeySet, publicAPIAllowedKeys)
		} else {
			resp, err = data.HandleMysekaiRequest(c, apiHelper, userID, server, requestKey)
		}
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
		if dataType != harukiUtils.UploadDataTypeMysekai {
			cacheKey := harukiRedis.CacheKeyBuilderWithAllowedQuery(c, "public_access", "key")
			_ = apiHelper.DBManager.Redis.SetCache(ctx, cacheKey, resp, 300*time.Second)
		}
		return c.JSON(resp)
	}
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

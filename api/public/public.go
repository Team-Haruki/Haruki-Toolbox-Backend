package public

import (
	"context"
	"fmt"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	harukiRedis "haruki-suite/utils/database/redis"
	"strconv"
	"time"

	"haruki-suite/utils/api/data"

	"github.com/gofiber/fiber/v3"
)

func validatePublicAPIAccess(record *postgresql.GameAccountBinding, dataType harukiUtils.UploadDataType) bool {
	if dataType == harukiUtils.UploadDataTypeSuite {
		return record.Suite != nil && record.Suite.AllowPublicApi
	}
	if dataType == harukiUtils.UploadDataTypeMysekai {
		return record.Mysekai != nil && record.Mysekai.AllowPublicApi
	}
	return false
}

func handlePublicDataRequest(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	allowedKeySet := make(map[string]struct{}, len(apiHelper.PublicAPIAllowedKeys))
	for _, k := range apiHelper.PublicAPIAllowedKeys {
		allowedKeySet[k] = struct{}{}
	}
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		server, dataType, userID, userIDStr, err := parseParams(c)
		if err != nil {
			return err
		}
		var resp any
		if dataType != harukiUtils.UploadDataTypeMysekai {
			cacheKey := harukiRedis.CacheKeyBuilder(c, "public_access")
			if found, err := apiHelper.DBManager.Redis.GetCache(ctx, cacheKey, &resp); err == nil && found {
				return c.JSON(resp)
			}
		}
		record, err := fetchAccountBinding(ctx, apiHelper, server, userIDStr)
		if err != nil {
			return harukiAPIHelper.ErrorNotFound(c, "account binding not found")
		}
		if !validatePublicAPIAccess(record, dataType) {
			return harukiAPIHelper.ErrorForbidden(c, "you are not allowed to access this player data.")
		}
		requestKey := c.Query("key")
		if dataType == harukiUtils.UploadDataTypeSuite {
			resp, err = data.HandleSuiteRequest(c, apiHelper, userID, server, requestKey, allowedKeySet, apiHelper.PublicAPIAllowedKeys)
		} else {
			resp, err = data.HandleMysekaiRequest(c, apiHelper, userID, server, requestKey)
		}
		if err != nil {
			if fErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fErr.Code, fErr.Message, nil)
			}
			return harukiAPIHelper.ErrorInternal(c, err.Error())
		}
		if dataType != harukiUtils.UploadDataTypeMysekai {
			cacheKey := harukiRedis.CacheKeyBuilder(c, "public_access")
			_ = apiHelper.DBManager.Redis.SetCache(ctx, cacheKey, resp, 300*time.Second)
		}
		return c.JSON(resp)
	}
}

func parseParams(c fiber.Ctx) (harukiUtils.SupportedDataUploadServer, harukiUtils.UploadDataType, int64, string, error) {
	serverStr := c.Params("server")
	server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
	if err != nil {
		return "", "", 0, "", err
	}
	dataTypeStr := c.Params("data_type")
	dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
	if err != nil {
		return "", "", 0, "", err
	}
	userIDStr := c.Params("user_id")
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		return "", "", 0, "", fmt.Errorf("Invalid userId, it must be integer")
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

func registerPublicRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	group := apiHelper.Router.Group("/public/:server/:data_type")
	group.Get("/:user_id", handlePublicDataRequest(apiHelper))
}

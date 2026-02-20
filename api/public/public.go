package public

import (
	"context"
	"fmt"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	harukiRedis "haruki-suite/utils/database/redis"
	harukiLogger "haruki-suite/utils/logger"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.mongodb.org/mongo-driver/v2/bson"
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

func compactFieldName(key string) string {
	if len(key) == 0 {
		return ""
	}
	return fmt.Sprintf("compact%s%s", string(key[0]-32), key[1:])
}

func buildSuiteProjection(keys []string) bson.M {
	proj := bson.M{"_id": 0}
	for _, key := range keys {
		if key == "userGamedata" {
			for _, field := range userGamedataAllowedFields {
				proj["userGamedata."+field] = 1
			}
		} else {
			proj[key] = 1
			proj[compactFieldName(key)] = 1
		}
	}
	return proj
}

func buildMysekaiProjection(keys []string) bson.M {
	if len(keys) == 0 {
		return bson.M{"_id": 0, "server": 0}
	}
	proj := bson.M{"_id": 0}
	for _, key := range keys {
		proj[key] = 1
	}
	return proj
}

func buildSuiteResponse(result bson.D, keys []string) bson.D {
	resp := make(bson.D, 0, len(keys))
	for _, key := range keys {
		if key == "userGamedata" {
			for _, elem := range result {
				if elem.Key == "userGamedata" {
					resp = append(resp, bson.E{Key: "userGamedata", Value: elem.Value})
					break
				}
			}
		} else {
			resp = append(resp, bson.E{Key: key, Value: GetValueFromResult(result, key)})
		}
	}
	return resp
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
			resp, err = handleSuiteRequest(c, apiHelper, userID, server, requestKey, allowedKeySet, apiHelper.PublicAPIAllowedKeys)
		} else {
			resp, err = handleMysekaiRequest(c, apiHelper, userID, server, requestKey)
		}
		if err != nil {
			return err
		}
		if dataType != harukiUtils.UploadDataTypeMysekai {
			cacheKey := harukiRedis.CacheKeyBuilder(c, "public_access")
			_ = apiHelper.DBManager.Redis.SetCache(ctx, cacheKey, resp, 300*time.Second)
		}
		return c.JSON(resp)
	}
}

func handleSuiteRequest(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID int64, server harukiUtils.SupportedDataUploadServer, requestKey string, allowedKeySet map[string]struct{}, allowedKeys []string) (any, error) {
	ctx := c.Context()

	var keys []string
	if requestKey == "" {
		keys = allowedKeys
	} else {
		keys = strings.Split(requestKey, ",")
		for _, key := range keys {
			if key == "userGamedata" {
				continue
			}
			if _, ok := allowedKeySet[key]; !ok {
				return nil, harukiAPIHelper.ErrorBadRequest(c, fmt.Sprintf("Invalid request key: %s", key))
			}
		}
	}

	projection := buildSuiteProjection(keys)
	result, err := apiHelper.DBManager.Mongo.GetDataWithProjection(ctx, userID, string(server), harukiUtils.UploadDataTypeSuite, projection)
	if err != nil {
		harukiLogger.Errorf("Failed to fetch mongo data: %v", err)
		return nil, harukiAPIHelper.ErrorInternal(c, fmt.Sprintf("Failed to get user data: %v", err))
	}
	if result == nil || len(result) == 0 {
		return nil, harukiAPIHelper.ErrorNotFound(c, "Player data not found.")
	}

	if requestKey != "" && len(keys) == 1 {
		key := keys[0]
		if key == "userGamedata" {
			for _, elem := range result {
				if elem.Key == "userGamedata" {
					return elem.Value, nil
				}
			}
			return bson.D{}, nil
		}
		return GetValueFromResult(result, key), nil
	}

	return buildSuiteResponse(result, keys), nil
}

func handleMysekaiRequest(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID int64, server harukiUtils.SupportedDataUploadServer, requestKey string) (any, error) {
	ctx := c.Context()

	var keys []string
	if requestKey != "" {
		keys = strings.Split(requestKey, ",")
	}

	projection := buildMysekaiProjection(keys)
	result, err := apiHelper.DBManager.Mongo.GetDataWithProjection(ctx, userID, string(server), harukiUtils.UploadDataTypeMysekai, projection)
	if err != nil {
		harukiLogger.Errorf("Failed to fetch mongo data: %v", err)
		return nil, harukiAPIHelper.ErrorInternal(c, fmt.Sprintf("Failed to get user data: %v", err))
	}
	if result == nil || len(result) == 0 {
		return nil, harukiAPIHelper.ErrorNotFound(c, "Player data not found.")
	}

	return result, nil
}

func parseParams(c fiber.Ctx) (harukiUtils.SupportedDataUploadServer, harukiUtils.UploadDataType, int64, string, error) {
	serverStr := c.Params("server")
	server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
	if err != nil {
		return "", "", 0, "", harukiAPIHelper.ErrorBadRequest(c, err.Error())
	}
	dataTypeStr := c.Params("data_type")
	dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
	if err != nil {
		return "", "", 0, "", harukiAPIHelper.ErrorBadRequest(c, err.Error())
	}
	userIDStr := c.Params("user_id")
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		return "", "", 0, "", harukiAPIHelper.ErrorBadRequest(c, "Invalid userId, it must be integer")
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

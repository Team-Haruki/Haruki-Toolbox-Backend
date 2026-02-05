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

func filterUserGamedata(result map[string]interface{}) map[string]interface{} {
	var gameData map[string]interface{}
	if val, ok := result["userGamedata"]; ok {
		if m, ok := val.(bson.M); ok {
			gameData = m
		} else if m, ok := val.(map[string]interface{}); ok {
			gameData = m
		}
	}

	if len(gameData) == 0 {
		return nil
	}

	filtered := map[string]interface{}{}
	for _, key := range []string{"userId", "name", "deck", "exp", "totalExp", "coin"} {
		if v, ok := gameData[key]; ok {
			filtered[key] = v
		}
	}
	return filtered
}

func processSingleKey(key string, result map[string]interface{}, filteredUserGamedata map[string]interface{}, allowedKeys []string) (interface{}, bool) {
	if !harukiAPIHelper.ArrayContains(allowedKeys, key) && key != "userGamedata" {
		return nil, false
	}
	if key == "userGamedata" {
		return filteredUserGamedata, true
	}
	return GetValueFromResult(result, key), true
}

func processMultipleKeys(keys []string, result map[string]interface{}, filteredUserGamedata map[string]interface{}, allowedKeys []string) (map[string]interface{}, string) {
	suite := map[string]interface{}{}
	includeUserGamedata := false
	for _, key := range keys {
		if key == "userGamedata" {
			includeUserGamedata = true
			continue
		}
		if !harukiAPIHelper.ArrayContains(allowedKeys, key) {
			return nil, key
		}
		suite[key] = GetValueFromResult(result, key)
	}
	if includeUserGamedata && filteredUserGamedata != nil {
		suite["userGamedata"] = filteredUserGamedata
	}
	return suite, ""
}

func buildDefaultSuiteResponse(result map[string]interface{}, allowedKeys []string) map[string]interface{} {
	suite := map[string]interface{}{}
	for _, key := range allowedKeys {
		if key == "userGamedata" {
			continue
		}
		suite[key] = GetValueFromResult(result, key)
	}
	return suite
}

func processSuiteData(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, result map[string]interface{}) (interface{}, error) {
	filteredUserGamedata := filterUserGamedata(result)
	requestKey := c.Query("key")
	if requestKey == "" {
		return buildDefaultSuiteResponse(result, apiHelper.PublicAPIAllowedKeys), nil
	}
	keys := strings.Split(requestKey, ",")
	if len(keys) == 1 {
		resp, valid := processSingleKey(keys[0], result, filteredUserGamedata, apiHelper.PublicAPIAllowedKeys)
		if !valid {
			return nil, harukiAPIHelper.ErrorBadRequest(c, "Invalid request key")
		}
		return resp, nil
	}
	suite, invalidKey := processMultipleKeys(keys, result, filteredUserGamedata, apiHelper.PublicAPIAllowedKeys)
	if invalidKey != "" {
		return nil, harukiAPIHelper.ErrorBadRequest(c, fmt.Sprintf("Invalid request key: %s", invalidKey))
	}
	return suite, nil
}

func processMysekaiData(c fiber.Ctx, result map[string]interface{}) interface{} {
	requestKey := c.Query("key")
	mysekaiData := map[string]interface{}{}
	if requestKey != "" {
		keys := strings.Split(requestKey, ",")
		for _, key := range keys {
			if key == "_id" || key == "policy" {
				continue
			}
			mysekaiData[key] = result[key]
		}
	} else {
		for k, v := range result {
			if k == "_id" || k == "policy" {
				continue
			}
			mysekaiData[k] = v
		}
	}
	return mysekaiData
}

func handlePublicDataRequest(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		server, dataType, userID, userIDStr, err := parseParams(c)
		if err != nil {
			return err
		}
		var resp interface{}
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
		result, err := fetchMongoData(c, apiHelper, userID, server, dataType)
		if err != nil {
			harukiLogger.Errorf("Failed to fetch mongo data: %v", err)
			return err
		}
		resp, err = processDataResponse(c, apiHelper, dataType, result)
		if err != nil {
			harukiLogger.Errorf("Failed to process data response: %v", err)
			return err
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

func fetchMongoData(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID int64, server harukiUtils.SupportedDataUploadServer, dataType harukiUtils.UploadDataType) (map[string]interface{}, error) {
	ctx := c.Context()
	result, err := apiHelper.DBManager.Mongo.GetData(ctx, userID, string(server), dataType)
	if err != nil {
		return nil, harukiAPIHelper.ErrorInternal(c, fmt.Sprintf("Failed to get user data: %v", err))
	}
	if result == nil {
		return nil, harukiAPIHelper.ErrorNotFound(c, "Player data not found.")
	}
	return result, nil
}

func processDataResponse(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dataType harukiUtils.UploadDataType, result map[string]interface{}) (interface{}, error) {
	if dataType == harukiUtils.UploadDataTypeSuite {
		return processSuiteData(c, apiHelper, result)
	}
	if dataType == harukiUtils.UploadDataTypeMysekai {
		return processMysekaiData(c, result), nil
	}
	return nil, harukiAPIHelper.ErrorInternal(c, "Unknown error.")
}

func registerPublicRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	group := apiHelper.Router.Group("/public/:server/:data_type")
	group.Get("/:user_id", handlePublicDataRequest(apiHelper))
}

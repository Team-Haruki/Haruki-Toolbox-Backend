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
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func handleGetPublicData(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		serverStr := c.Params("server")
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
		}

		dataTypeStr := c.Params("data_type")
		dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
		}

		userIDStr := c.Params("user_id")
		userID, err := strconv.ParseInt(userIDStr, 10, 64)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid userId, it must be integer", nil)
		}

		cacheKey := harukiRedis.CacheKeyBuilder(c, "public_access")
		ctx := context.Background()
		var resp interface{}
		if found, err := apiHelper.DBManager.Redis.GetCache(ctx, cacheKey, &resp); err == nil && found {
			return c.JSON(resp)
		}

		record, err := fetchGameAccountBinding(c, ctx, apiHelper, server, userIDStr)
		if err != nil {
			return err
		}

		result, err := fetchUserData(c, apiHelper, userID, server, dataType)
		if err != nil {
			return err
		}

		if err := validatePublicAPIAccess(c, record, dataType); err != nil {
			return err
		}

		resp, err = processDataByType(c, apiHelper, dataType, result)
		if err != nil {
			return err
		}

		_ = apiHelper.DBManager.Redis.SetCache(ctx, cacheKey, resp, 300*time.Second)

		return c.JSON(resp)
	}
}

func fetchGameAccountBinding(c fiber.Ctx, ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, server harukiUtils.SupportedDataUploadServer, userIDStr string) (*postgresql.GameAccountBinding, error) {
	record, err := apiHelper.DBManager.DB.GameAccountBinding.
		Query().
		Where(
			gameaccountbinding.ServerEQ(string(server)),
			gameaccountbinding.GameUserIDEQ(userIDStr),
		).
		WithUser().
		Only(ctx)
	if err != nil {
		return nil, harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusNotFound, "account binding not found", nil)
	}
	return record, nil
}

func fetchUserData(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID int64, server harukiUtils.SupportedDataUploadServer, dataType harukiUtils.UploadDataType) (map[string]interface{}, error) {
	result, err := apiHelper.DBManager.Mongo.GetData(c.RequestCtx(), userID, string(server), dataType)
	if err != nil {
		return nil, harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, fmt.Sprintf("Failed to get user data: %v", err), nil)
	}
	if result == nil {
		return nil, harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusNotFound, "Player data not found.", nil)
	}
	return result, nil
}

func validatePublicAPIAccess(c fiber.Ctx, record *postgresql.GameAccountBinding, dataType harukiUtils.UploadDataType) error {
	if dataType == harukiUtils.UploadDataTypeSuite {
		if record.Suite == nil || !record.Suite.AllowPublicApi {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, "you are not allowed to access this player data.", nil)
		}
	} else if dataType == harukiUtils.UploadDataTypeMysekai {
		if record.Mysekai == nil || !record.Mysekai.AllowPublicApi {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, "you are not allowed to access this player data.", nil)
		}
	}
	return nil
}

func processDataByType(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dataType harukiUtils.UploadDataType, result map[string]interface{}) (interface{}, error) {
	if dataType == harukiUtils.UploadDataTypeSuite {
		return processSuiteData(c, result, apiHelper)
	} else if dataType == harukiUtils.UploadDataTypeMysekai {
		return processMysekaiData(c, result), nil
	}
	return nil, harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Unknown error.", nil)
}

func processSuiteData(c fiber.Ctx, result map[string]interface{}, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) (interface{}, error) {
	filteredUserGamedata := extractFilteredUserGamedata(result)
	requestKey := c.Query("key")

	if requestKey == "" {
		return buildDefaultSuiteResponse(result, apiHelper), nil
	}

	return processRequestedSuiteKeys(c, requestKey, result, filteredUserGamedata, apiHelper)
}

func extractFilteredUserGamedata(result map[string]interface{}) map[string]interface{} {
	gameData, ok := result["userGamedata"].(primitive.M)
	if !ok || len(gameData) == 0 {
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

func processRequestedSuiteKeys(c fiber.Ctx, requestKey string, result map[string]interface{}, filteredUserGamedata map[string]interface{}, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) (interface{}, error) {
	keys := strings.Split(requestKey, ",")

	if len(keys) == 1 {
		return processSingleSuiteKey(c, keys[0], result, filteredUserGamedata, apiHelper)
	}

	return processMultipleSuiteKeys(c, keys, result, filteredUserGamedata, apiHelper)
}

func processSingleSuiteKey(c fiber.Ctx, key string, result map[string]interface{}, filteredUserGamedata map[string]interface{}, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) (interface{}, error) {
	if !harukiAPIHelper.ArrayContains(apiHelper.PublicAPIAllowedKeys, key) && key != "userGamedata" {
		return nil, harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, "Invalid request key", nil)
	}
	if key == "userGamedata" {
		return filteredUserGamedata, nil
	}
	return GetValueFromResult(result, key), nil
}

func processMultipleSuiteKeys(c fiber.Ctx, keys []string, result map[string]interface{}, filteredUserGamedata map[string]interface{}, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) (interface{}, error) {
	suite := map[string]interface{}{}
	includeUserGamedata := false

	for _, key := range keys {
		if key == "userGamedata" {
			includeUserGamedata = true
			continue
		}
		if !harukiAPIHelper.ArrayContains(apiHelper.PublicAPIAllowedKeys, key) {
			return nil, harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, fmt.Sprintf("Invalid request key: %s", key), nil)
		}
		suite[key] = GetValueFromResult(result, key)
	}

	if includeUserGamedata && filteredUserGamedata != nil {
		suite["userGamedata"] = filteredUserGamedata
	}
	return suite, nil
}

func buildDefaultSuiteResponse(result map[string]interface{}, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) map[string]interface{} {
	suite := map[string]interface{}{}
	for _, key := range apiHelper.PublicAPIAllowedKeys {
		if key == "userGamedata" {
			continue
		}
		suite[key] = GetValueFromResult(result, key)
	}
	return suite
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

func registerPublicRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	group := apiHelper.Router.Group("/public/:server/:data_type")
	group.Get("/:user_id", handleGetPublicData(apiHelper))
}

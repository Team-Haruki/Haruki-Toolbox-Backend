package public

import (
	"context"
	"fmt"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	harukiRedis "haruki-suite/utils/database/redis"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func handleGetPublicData(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		server, dataType, userID, userIDStr, err := parseRequestParams(c)
		if err != nil {
			return err
		}

		cacheKey := harukiRedis.CacheKeyBuilder(c, "public_access")
		ctx := context.Background()
		var resp interface{}
		if found, err := apiHelper.DBManager.Redis.GetCache(ctx, cacheKey, &resp); err == nil && found {
			return c.JSON(resp)
		}

		record, err := fetchGameAccountBinding(ctx, apiHelper, server, userIDStr)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusNotFound, err.Error(), nil)
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

func parseRequestParams(c fiber.Ctx) (harukiUtils.SupportedDataUploadServer, harukiUtils.UploadDataType, int64, string, error) {
	serverStr := c.Params("server")
	server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
	if err != nil {
		return "", "", 0, "", harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
	}

	dataTypeStr := c.Params("data_type")
	dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
	if err != nil {
		return "", "", 0, "", harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
	}

	userIDStr := c.Params("user_id")
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		return "", "", 0, "", harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Invalid userId, it must be integer", nil)
	}

	return server, dataType, userID, userIDStr, nil
}

func fetchGameAccountBinding(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, server harukiUtils.SupportedDataUploadServer, userIDStr string) (interface{}, error) {
	record, err := apiHelper.DBManager.DB.GameAccountBinding.
		Query().
		Where(
			gameaccountbinding.ServerEQ(string(server)),
			gameaccountbinding.GameUserIDEQ(userIDStr),
		).
		WithUser().
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("account binding not found")
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

func validatePublicAPIAccess(c fiber.Ctx, record interface{}, dataType harukiUtils.UploadDataType) error {
	recordValue := reflect.ValueOf(record)
	if recordValue.Kind() == reflect.Ptr {
		recordValue = recordValue.Elem()
	}

	var allowPublicApi bool
	if dataType == harukiUtils.UploadDataTypeSuite {
		suiteField := recordValue.FieldByName("Suite")
		if !suiteField.IsValid() || suiteField.IsNil() {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, "you are not allowed to access this player data.", nil)
		}
		allowField := suiteField.Elem().FieldByName("AllowPublicApi")
		if !allowField.IsValid() {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, "you are not allowed to access this player data.", nil)
		}
		allowPublicApi = allowField.Bool()
	} else if dataType == harukiUtils.UploadDataTypeMysekai {
		mysekaiField := recordValue.FieldByName("Mysekai")
		if !mysekaiField.IsValid() || mysekaiField.IsNil() {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, "you are not allowed to access this player data.", nil)
		}
		allowField := mysekaiField.Elem().FieldByName("AllowPublicApi")
		if !allowField.IsValid() {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, "you are not allowed to access this player data.", nil)
		}
		allowPublicApi = allowField.Bool()
	}

	if !allowPublicApi {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, "you are not allowed to access this player data.", nil)
	}

	return nil
}

func processDataByType(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dataType harukiUtils.UploadDataType, result map[string]interface{}) (interface{}, error) {
	if dataType == harukiUtils.UploadDataTypeSuite {
		return processSuiteData(c, apiHelper, result)
	}
	if dataType == harukiUtils.UploadDataTypeMysekai {
		return processMysekaiData(c, result)
	}
	return nil, harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Unknown error.", nil)
}

func processSuiteData(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, result map[string]interface{}) (interface{}, error) {
	filteredUserGamedata := extractFilteredUserGamedata(result)
	requestKey := c.Query("key")

	if requestKey == "" {
		return buildDefaultSuiteResponse(apiHelper, result), nil
	}

	return processSuiteKeys(c, apiHelper, requestKey, result, filteredUserGamedata)
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

func processSuiteKeys(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, requestKey string, result map[string]interface{}, filteredUserGamedata map[string]interface{}) (interface{}, error) {
	keys := strings.Split(requestKey, ",")

	if len(keys) == 1 {
		return processSingleSuiteKey(c, apiHelper, keys[0], result, filteredUserGamedata)
	}

	return processMultipleSuiteKeys(c, apiHelper, keys, result, filteredUserGamedata)
}

func processSingleSuiteKey(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, key string, result map[string]interface{}, filteredUserGamedata map[string]interface{}) (interface{}, error) {
	if !harukiAPIHelper.ArrayContains(apiHelper.PublicAPIAllowedKeys, key) && key != "userGamedata" {
		return nil, harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, "Invalid request key", nil)
	}
	if key == "userGamedata" {
		return filteredUserGamedata, nil
	}
	return GetValueFromResult(result, key), nil
}

func processMultipleSuiteKeys(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, keys []string, result map[string]interface{}, filteredUserGamedata map[string]interface{}) (interface{}, error) {
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

func buildDefaultSuiteResponse(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, result map[string]interface{}) map[string]interface{} {
	suite := map[string]interface{}{}
	for _, key := range apiHelper.PublicAPIAllowedKeys {
		if key == "userGamedata" {
			continue
		}
		suite[key] = GetValueFromResult(result, key)
	}
	return suite
}

func processMysekaiData(c fiber.Ctx, result map[string]interface{}) (interface{}, error) {
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
	return mysekaiData, nil
}

func registerPublicRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	group := apiHelper.Router.Group("/public/:server/:data_type")
	group.Get("/:user_id", handleGetPublicData(apiHelper))
}

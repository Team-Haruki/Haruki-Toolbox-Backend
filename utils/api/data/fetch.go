package data

import (
	"fmt"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	harukiLogger "haruki-suite/utils/logger"
	"strings"

	"github.com/gofiber/fiber/v3"
	"go.mongodb.org/mongo-driver/v2/bson"
)

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

func HandleSuiteRequest(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID int64, server harukiUtils.SupportedDataUploadServer, requestKey string, allowedKeySet map[string]struct{}, allowedKeys []string) (any, error) {
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
				return nil, fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("Invalid request key: %s", key))
			}
		}
	}

	projection := buildSuiteProjection(keys)
	result, err := apiHelper.DBManager.Mongo.GetDataWithProjection(ctx, userID, string(server), harukiUtils.UploadDataTypeSuite, projection)
	if err != nil {
		harukiLogger.Errorf("Failed to fetch mongo data: %v", err)
		return nil, fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("Failed to get user data: %v", err))
	}
	if result == nil || len(result) == 0 {
		return nil, fiber.NewError(fiber.StatusNotFound, "Player data not found.")
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

func HandleMysekaiRequest(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID int64, server harukiUtils.SupportedDataUploadServer, requestKey string) (any, error) {
	ctx := c.Context()

	var keys []string
	if requestKey != "" {
		keys = strings.Split(requestKey, ",")
	}

	projection := buildMysekaiProjection(keys)
	result, err := apiHelper.DBManager.Mongo.GetDataWithProjection(ctx, userID, string(server), harukiUtils.UploadDataTypeMysekai, projection)
	if err != nil {
		harukiLogger.Errorf("Failed to fetch mongo data: %v", err)
		return nil, fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("Failed to get user data: %v", err))
	}
	if result == nil || len(result) == 0 {
		return nil, fiber.NewError(fiber.StatusNotFound, "Player data not found.")
	}

	return result, nil
}

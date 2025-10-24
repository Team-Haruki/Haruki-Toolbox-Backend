package public

import (
	"fmt"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	harukiRedis "haruki-suite/utils/database/redis"
	"strconv"
	"strings"
	"time"

	"context"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func registerPublicRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	group := apiHelper.Router.Group("/public/:server/:data_type")

	group.Get("/:user_id", func(c *fiber.Ctx) error {
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

		record, err := apiHelper.DBManager.DB.GameAccountBinding.
			Query().
			Where(
				gameaccountbinding.ServerEQ(string(server)),
				gameaccountbinding.GameUserIDEQ(userIDStr),
			).
			WithUser().
			Only(ctx)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusNotFound, "account binding not found", nil)
		}

		result, err := apiHelper.DBManager.Mongo.GetData(c.Context(), userID, string(server), dataType)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, fmt.Sprintf("Failed to get user data: %v", err), nil)
		}
		if result == nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusNotFound, "Player data not found.", nil)
		}

		if dataType == harukiUtils.UploadDataTypeSuite {
			if record.Suite == nil || !record.Suite.AllowPublicApi {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, "you are not allowed to access this player data.", nil)
			}
		} else if dataType == harukiUtils.UploadDataTypeMysekai {
			if record.Mysekai == nil || !record.Mysekai.AllowPublicApi {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, "you are not allowed to access this player data.", nil)
			}
		}

		if dataType == harukiUtils.UploadDataTypeSuite {
			suite := map[string]interface{}{}
			var filteredUserGamedata map[string]interface{}
			if gameData, ok := result["userGamedata"].(primitive.M); ok && len(gameData) > 0 {
				filtered := map[string]interface{}{}
				for _, key := range []string{"userId", "name", "deck", "exp", "totalExp", "coin"} {
					if v, ok := gameData[key]; ok {
						filtered[key] = v
					}
				}
				filteredUserGamedata = filtered
			}
			requestKey := c.Query("key")
			if requestKey != "" {
				keys := strings.Split(requestKey, ",")
				includeUserGamedata := false
				for _, key := range keys {
					if key == "userGamedata" {
						includeUserGamedata = true
						break
					}
				}
				if len(keys) == 1 {
					key := keys[0]
					if !harukiAPIHelper.ArrayContains(apiHelper.PublicAPIAllowedKeys, key) && key != "userGamedata" {
						return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, "Invalid request key", nil)
					}
					if key == "userGamedata" {
						resp = filteredUserGamedata
					} else {
						resp = GetValueFromResult(result, key)
					}
				} else {
					for _, key := range keys {
						if key == "userGamedata" {
							continue
						}
						if !harukiAPIHelper.ArrayContains(apiHelper.PublicAPIAllowedKeys, key) {
							return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, fmt.Sprintf("Invalid request key: %s", key), nil)
						}
						suite[key] = GetValueFromResult(result, key)
					}
					if includeUserGamedata && filteredUserGamedata != nil {
						suite["userGamedata"] = filteredUserGamedata
					}
					resp = suite
				}
			} else {
				for _, key := range apiHelper.PublicAPIAllowedKeys {
					if key == "userGamedata" {
						continue
					}
					suite[key] = GetValueFromResult(result, key)
				}
				resp = suite
			}
		} else if dataType == harukiUtils.UploadDataTypeMysekai {
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
			resp = mysekaiData
		} else {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusInternalServerError, "Unknown error.", nil)
		}

		apiHelper.DBManager.Redis.SetCache(ctx, cacheKey, resp, 300*time.Second)

		return c.JSON(resp)
	})
}

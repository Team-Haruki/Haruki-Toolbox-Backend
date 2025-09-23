package public

import (
	"fmt"
	harukiRootApi "haruki-suite/api"
	harukiUtils "haruki-suite/utils"
	harukiMongo "haruki-suite/utils/mongo"
	harukiRedis "haruki-suite/utils/redis"
	"strconv"
	"strings"
	"time"

	"context"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

type HarukiPublicAPI struct {
	Mongo       *harukiMongo.MongoDBManager
	Redis       *redis.Client
	AllowedKeys []string
}

func (api *HarukiPublicAPI) RegisterRoutes(app *fiber.App) {
	group := app.Group("/public/:server/:data_type")

	group.Get("/:user_id", func(c *fiber.Ctx) error {
		serverStr := c.Params("server")
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
				Message: err.Error(),
			})
		}

		dataTypeStr := c.Params("data_type")
		dataType, err := harukiUtils.ParseUploadDataType(dataTypeStr)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
				Message: err.Error(),
			})
		}

		userIDStr := c.Params("user_id")
		userID, err := strconv.ParseInt(userIDStr, 10, 64)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
				Message: "Invalid userId, it must be integer",
			})
		}

		cacheKey := harukiRedis.CacheKeyBuilder(c, "public_access")
		ctx := context.Background()
		var resp interface{}
		if found, err := harukiRedis.GetCache(ctx, api.Redis, cacheKey, &resp); err == nil && found {
			return c.JSON(resp)
		}

		result, err := api.Mongo.GetData(c.Context(), userID, string(server), dataType)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(fiber.StatusInternalServerError),
				Message: fmt.Sprintf("Failed to get user data: %v", err),
			})
		}
		if result == nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(fiber.StatusNotFound),
				Message: "Player data not found.",
			})
		}
		if policy, ok := result["policy"].(string); ok && policy == string(harukiUtils.UploadPolicyPrivate) {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(fiber.StatusForbidden),
				Message: "This player's data is not publicly accessible.",
			})
		}

		if dataType == harukiUtils.UploadDataTypeSuite {
			suite := map[string]interface{}{}
			if gameData, ok := result["userGamedata"].(map[string]interface{}); ok && len(gameData) > 0 {
				filtered := map[string]interface{}{}
				for _, key := range []string{"userId", "name", "deck", "exp", "totalExp"} {
					if v, ok := gameData[key]; ok {
						filtered[key] = v
					}
				}
				suite["userGamedata"] = filtered
			}
			allowedKeys := api.AllowedKeys
			requestKey := c.Query("key")
			if requestKey != "" {
				keys := strings.Split(requestKey, ",")
				if len(keys) == 1 {
					key := keys[0]
					if !harukiUtils.ArrayContains(allowedKeys, key) {
						return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
							Status:  harukiRootApi.IntPtr(fiber.StatusForbidden),
							Message: "Invalid request key",
						})
					}
					resp = harukiUtils.GetValueFromResult(result, key)
				} else {
					for _, key := range keys {
						if key == "userGamedata" {
							continue
						}
						if !harukiUtils.ArrayContains(allowedKeys, key) {
							return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
								Status:  harukiRootApi.IntPtr(fiber.StatusForbidden),
								Message: fmt.Sprintf("Invalid request key: %s", key),
							})
						}
						suite[key] = harukiUtils.GetValueFromResult(result, key)
					}
					resp = suite
				}
			} else {
				for _, key := range allowedKeys {
					suite[key] = harukiUtils.GetValueFromResult(result, key)
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
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(fiber.StatusInternalServerError),
				Message: "Unknown error.",
			})
		}

		_ = harukiRedis.SetCache(ctx, api.Redis, cacheKey, resp, 300*time.Second)

		return c.JSON(resp)
	})
}

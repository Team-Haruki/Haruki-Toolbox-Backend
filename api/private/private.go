package private

import (
	"fmt"
	harukiRootApi "haruki-suite/api"
	harukiUtils "haruki-suite/utils"
	harukiMongo "haruki-suite/utils/mongo"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
)

func ValidateUserPermission(expectedToken, requiredAgentKeyword string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		authorization := c.Get("Authorization")
		userAgent := c.Get("User-Agent")

		if authorization != expectedToken {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(fiber.StatusUnauthorized),
				Message: "Invalid Authorization header",
			})
		}

		if requiredAgentKeyword != "" && !harukiUtils.StringContains(userAgent, requiredAgentKeyword) {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(fiber.StatusForbidden),
				Message: "User-Agent not allowed",
			})
		}
		return c.Next()
	}
}

func RegisterRoutes(app *fiber.App, manager *harukiMongo.MongoDBManager, expectedToken, requiredAgentKeyword string) {
	api := app.Group("/private/:server/:data_type", ValidateUserPermission(expectedToken, requiredAgentKeyword))

	api.Get("/:user_id", func(c *fiber.Ctx) error {
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
		userID, err := strconv.Atoi(userIDStr)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
				Message: "Invalid user_id, must be integer",
			})
		}

		result, err := manager.GetData(c.Context(), int64(userID), string(server), dataType)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(fiber.StatusInternalServerError),
				Message: fmt.Sprintf("Failed to get user data: %v", err),
			})
		}

		requestKey := c.Query("key")
		if requestKey != "" {
			keys := strings.Split(requestKey, ",")
			if len(keys) == 1 {
				return c.JSON(result[keys[0]])
			}
			data := make(map[string]interface{})
			for _, k := range keys {
				data[k] = result[k]
			}
			return c.JSON(data)
		}

		if result == nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Status:  harukiRootApi.IntPtr(fiber.StatusNotFound),
				Message: "Player data not found.",
			})
		}

		return c.JSON(result)
	})
}

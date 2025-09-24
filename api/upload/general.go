package upload

import (
	"context"
	"fmt"
	harukiRootApi "haruki-suite/api"
	harukiUtils "haruki-suite/utils"
	harukiMongo "haruki-suite/utils/mongo"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

func requireUploadType(expectedType harukiUtils.UploadDataType) fiber.Handler {
	return func(c *fiber.Ctx) error {
		uploadType := harukiUtils.UploadDataType(c.Params("upload_type"))

		if uploadType != expectedType {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: "Invalid upload_type: expected " + string(expectedType),
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
			})
		}

		return c.Next()
	}
}

func registerGeneralRoutes(app *fiber.App, mongoManager *harukiMongo.MongoDBManager, redisClient *redis.Client) {
	api := app.Group("/general/:server/:upload_type/:policy")

	api.Post("/upload", requireUploadType(harukiUtils.UploadDataTypeSuite), func(c *fiber.Ctx) error {
		serverStr := c.Params("server")
		policyStr := c.Params("policy")

		_, err := harukiUtils.ParseUploadPolicy(policyStr)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: err.Error(),
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
			})
		}

		result, err := HandleUpload(
			context.Background(),
			c.Request().Body(),
			serverStr,
			policyStr,
			mongoManager,
			redisClient,
			string(harukiUtils.UploadDataTypeSuite),
			nil,
		)

		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: err.Error(),
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
			})
		}

		if result.UserID != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: fmt.Sprintf("%s server user %d successfully uploaded suite data.", serverStr, *result.UserID),
			})
		} else {
			fmt.Println("Debug: UserID is nil")
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: fmt.Sprintf("%s server user with unknown ID successfully uploaded suite data.", serverStr),
			})
		}
	})

	api.Post("/:user_id/upload", requireUploadType(harukiUtils.UploadDataTypeMysekai), func(c *fiber.Ctx) error {
		serverStr := c.Params("server")
		policyStr := c.Params("policy")
		userIdStr := c.Params("user_id")

		_, err := harukiUtils.ParseUploadPolicy(policyStr)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: err.Error(),
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
			})
		}

		userId, err := strconv.ParseInt(userIdStr, 10, 64)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: err.Error(),
			})
		}

		result, err := HandleUpload(
			context.Background(),
			c.Request().Body(),
			serverStr,
			policyStr,
			mongoManager,
			redisClient,
			string(harukiUtils.UploadDataTypeMysekai),
			&userId,
		)

		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: err.Error(),
				Status:  harukiRootApi.IntPtr(fiber.StatusBadRequest),
			})
		}

		if result.UserID != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: fmt.Sprintf("%s server user %d successfully uploaded mysekai data.", serverStr, *result.UserID),
			})
		} else {
			fmt.Println("Debug: UserID is nil")
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: fmt.Sprintf("%s server user with unknown ID successfully uploaded mysekai data.", serverStr),
			})
		}
	})

}

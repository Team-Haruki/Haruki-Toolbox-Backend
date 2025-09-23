package upload

import (
	"context"
	"fmt"
	harukiRootApi "haruki-suite/api"
	harukiUtils "haruki-suite/utils"
	harukiMongo "haruki-suite/utils/mongo"
	harukiSekai "haruki-suite/utils/sekai"
	"net/http"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

func registerInheritRoutes(app *fiber.App, mongoManager *harukiMongo.MongoDBManager, redisClient *redis.Client) {
	api := app.Group("/general/:server/:upload_type/:policy")

	api.Post("/submit_inherit", requireUploadType(harukiUtils.UploadDataTypeMysekai), func(c *fiber.Ctx) error {
		serverStr := c.Params("server")
		policyStr := c.Params("policy")
		userIdStr := c.Params("user_id")
		uploadTypeStr := c.Params("upload_type")

		server, err := harukiUtils.ParseSupportedInheritUploadServer(serverStr)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: err.Error(),
				Status:  harukiRootApi.IntPtr(http.StatusBadRequest),
			})
		}
		policy, err := harukiUtils.ParseUploadPolicy(policyStr)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: err.Error(),
				Status:  harukiRootApi.IntPtr(http.StatusBadRequest),
			})
		}
		uploadType, err := harukiUtils.ParseUploadDataType(uploadTypeStr)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: err.Error(),
				Status:  harukiRootApi.IntPtr(http.StatusBadRequest),
			})
		}
		userId, err := strconv.ParseInt(userIdStr, 10, 64)
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: err.Error(),
				Status:  harukiRootApi.IntPtr(http.StatusBadRequest),
			})
		}
		data := new(harukiUtils.InheritInformation)
		if c.BodyParser(data) != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: "Validation error: " + err.Error(),
				Status:  harukiRootApi.IntPtr(http.StatusUnprocessableEntity),
			})
		}

		if harukiUtils.SupportedInheritUploadServer(serverStr) == harukiUtils.SupportedInheritUploadServerEN &&
			uploadType == harukiUtils.UploadDataTypeMysekai {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: "Haruki Inherit can not accept EN server's mysekai data upload request at this time.",
				Status:  harukiRootApi.IntPtr(http.StatusForbidden),
			})
		}

		retriever := harukiSekai.NewSekaiDataRetriever(
			harukiUtils.SupportedInheritUploadServer(serverStr),
			*data,
			policy,
			uploadType,
		)
		result, err := retriever.Run(context.Background())
		if err != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: err.Error(),
				Status:  harukiRootApi.IntPtr(http.StatusBadRequest),
			})
		}

		var uploadErr error
		if uploadType == harukiUtils.UploadDataTypeMysekai {
			if result.Mysekai == nil {
				return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
					Message: "Retrieve mysekai data failed.",
					Status:  harukiRootApi.IntPtr(http.StatusBadRequest),
				})
			}
			_, uploadErr = HandleUpload(
				context.Background(),
				result.Mysekai,
				string(server),
				string(policy),
				mongoManager,
				redisClient,
				string(uploadType),
				userId,
			)
		}
		if uploadType == harukiUtils.UploadDataTypeSuite {
			if result.Suite == nil {
				return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
					Message: "Retrieve suite data failed.",
					Status:  harukiRootApi.IntPtr(http.StatusBadRequest),
				})
			}
			_, uploadErr = HandleUpload(
				context.Background(),
				result.Suite,
				string(server),
				string(policy),
				mongoManager,
				redisClient,
				string(uploadType),
				userId,
			)
		}

		if uploadErr != nil {
			return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
				Message: uploadErr.Error(),
				Status:  harukiRootApi.IntPtr(http.StatusBadRequest),
			})
		}
		return harukiRootApi.JSONResponse(c, harukiUtils.APIResponse{
			Message: fmt.Sprintf("%s server user %d successfully uploaded data.", serverStr, result.UserID),
		})
	})

}

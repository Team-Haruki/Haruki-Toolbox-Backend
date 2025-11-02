package upload

import (
	"context"
	"fmt"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	harukiSekai "haruki-suite/utils/sekai"

	"github.com/gofiber/fiber/v2"
)

func handleInheritSubmit(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c *fiber.Ctx) error {
		serverStr := c.Params("server")
		uploadTypeStr := c.Params("upload_type")

		server, err := harukiUtils.ParseSupportedInheritUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
		}
		uploadType, err := harukiUtils.ParseUploadDataType(uploadTypeStr)
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
		}
		data := new(harukiUtils.InheritInformation)
		if err := c.BodyParser(data); err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusUnprocessableEntity, "Validation error: "+err.Error(), nil)
		}

		if harukiUtils.SupportedInheritUploadServer(serverStr) == harukiUtils.SupportedInheritUploadServerEN &&
			uploadType == harukiUtils.UploadDataTypeMysekai {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusForbidden, "Haruki Inherit can not accept EN server's mysekai data upload request at this time.", nil)
		}

		retriever := harukiSekai.NewSekaiDataRetriever(server, *data, uploadType)
		result, err := retriever.Run(context.Background())
		if err != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
		}

		if err := uploadMysekaiDataIfNeeded(c, apiHelper, uploadType, result, serverStr); err != nil {
			return err
		}

		if err := uploadSuiteData(c, apiHelper, result, serverStr); err != nil {
			return err
		}

		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusOK, fmt.Sprintf("%s server user %d successfully uploaded data.", serverStr, result.UserID), nil)
	}
}

func uploadMysekaiDataIfNeeded(c *fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, uploadType harukiUtils.UploadDataType, result *harukiUtils.SekaiInheritDataRetrieverResponse, serverStr string) error {
	if uploadType != harukiUtils.UploadDataTypeMysekai {
		return nil
	}

	if result.Mysekai == nil {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Retrieve mysekai data failed, it seems you may not have completed the tutorial yet.", nil)
	}

	_, err := HandleUpload(
		context.Background(),
		result.Mysekai,
		harukiUtils.SupportedDataUploadServer(serverStr),
		harukiUtils.UploadDataTypeMysekai,
		&result.UserID,
		nil,
		apiHelper,
	)
	if err != nil {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
	}
	return nil
}

func uploadSuiteData(c *fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, result *harukiUtils.SekaiInheritDataRetrieverResponse, serverStr string) error {
	if result.Suite == nil {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, "Retrieve suite data failed: unknown error", nil)
	}

	_, err := HandleUpload(
		context.Background(),
		result.Suite,
		harukiUtils.SupportedDataUploadServer(serverStr),
		harukiUtils.UploadDataTypeSuite,
		&result.UserID,
		nil,
		apiHelper,
	)
	if err != nil {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadRequest, err.Error(), nil)
	}
	return nil
}

func registerInheritRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	api := apiHelper.Router.Group("/inherit/:server/:upload_type")

	api.Post("/submit", handleInheritSubmit(apiHelper))
}

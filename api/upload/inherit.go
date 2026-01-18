package upload

import (
	"fmt"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	harukiSekai "haruki-suite/utils/sekai"

	"github.com/gofiber/fiber/v3"
)

func handleInheritSubmit(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		serverStr := c.Params("server")
		uploadTypeStr := c.Params("upload_type")
		server, err := harukiUtils.ParseSupportedInheritUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}
		uploadType, err := harukiUtils.ParseUploadDataType(uploadTypeStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}
		data := new(harukiUtils.InheritInformation)
		if err := c.Bind().Body(data); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "Validation error: "+err.Error())
		}
		retriever := harukiSekai.NewSekaiDataRetriever(server, *data, uploadType)
		result, err := retriever.Run(ctx)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, err.Error())
		}
		if err := uploadMysekaiDataIfNeeded(c, apiHelper, uploadType, result, serverStr); err != nil {
			return err
		}
		if err := uploadSuiteData(c, apiHelper, result, serverStr); err != nil {
			return err
		}
		return harukiAPIHelper.SuccessResponse[string](c, fmt.Sprintf("%s server user %d successfully uploaded data.", serverStr, result.UserID), nil)
	}
}

func uploadMysekaiDataIfNeeded(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, uploadType harukiUtils.UploadDataType, result *harukiUtils.SekaiInheritDataRetrieverResponse, serverStr string) error {
	ctx := c.Context()
	if uploadType != harukiUtils.UploadDataTypeMysekai {
		return nil
	}
	if result.Mysekai == nil {
		return harukiAPIHelper.ErrorBadRequest(c, "Retrieve mysekai data failed, it seems you may not have completed the tutorial yet.")
	}
	_, err := HandleUpload(
		ctx,
		result.Mysekai,
		harukiUtils.SupportedDataUploadServer(serverStr),
		harukiUtils.UploadDataTypeMysekai,
		&result.UserID,
		nil,
		apiHelper,
		harukiUtils.UploadMethodInherit,
	)
	if err != nil {
		return harukiAPIHelper.ErrorBadRequest(c, err.Error())
	}
	return nil
}

func uploadSuiteData(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, result *harukiUtils.SekaiInheritDataRetrieverResponse, serverStr string) error {
	ctx := c.Context()
	if result.Suite == nil {
		return harukiAPIHelper.ErrorBadRequest(c, "Retrieve suite data failed: unknown error")
	}
	_, err := HandleUpload(
		ctx,
		result.Suite,
		harukiUtils.SupportedDataUploadServer(serverStr),
		harukiUtils.UploadDataTypeSuite,
		&result.UserID,
		nil,
		apiHelper,
		harukiUtils.UploadMethodInherit,
	)
	if err != nil {
		return harukiAPIHelper.ErrorBadRequest(c, err.Error())
	}
	return nil
}

func registerInheritRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	api := apiHelper.Router.Group("/inherit/:server/:upload_type")

	api.Post("/submit", handleInheritSubmit(apiHelper))
}

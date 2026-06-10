package upload

import (
	"errors"
	"fmt"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/sekai"

	"github.com/gofiber/fiber/v3"
)

func handleInheritSubmit(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		serverStr := c.Params("server")
		uploadTypeStr := c.Params("upload_type")
		server, err := harukiUtils.ParseSupportedInheritUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid server")
		}
		uploadType, err := harukiUtils.ParseUploadDataType(uploadTypeStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid upload_type")
		}
		data := new(harukiUtils.InheritInformation)
		if err := c.Bind().Body(data); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}
		retriever := harukiSekai.NewSekaiDataRetriever(server, *data, uploadType)
		result, err := retriever.Run(ctx)
		if err != nil {
			uploadServer := harukiUtils.SupportedDataUploadServer(server)
			recordInheritRetrievalFailure(apiHelper, uploadServer, uploadType, result, err)
			return harukiAPIHelper.ErrorBadRequest(c, "failed to retrieve game data")
		}
		uploadServer := harukiUtils.SupportedDataUploadServer(server)
		if err := uploadMysekaiDataIfNeeded(c, apiHelper, uploadType, result, uploadServer); err != nil {
			return err
		}
		if err := uploadSuiteData(c, apiHelper, result, uploadServer); err != nil {
			return err
		}
		return harukiAPIHelper.SuccessResponse[string](c, fmt.Sprintf("%s server user %d successfully uploaded data.", serverStr, result.UserID), nil)
	}
}

func recordInheritRetrievalFailure(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, server harukiUtils.SupportedDataUploadServer, uploadType harukiUtils.UploadDataType, result *harukiUtils.SekaiInheritDataRetrieverResponse, err error) {
	if result == nil || result.UserID <= 0 {
		sharedDataHandlerLogger.Warnf("Skip inherit retrieval failure upload log because game user ID is unavailable: %v", err)
		return
	}
	dataType := inheritRetrievalFailureDataType(uploadType, err)
	uploadCtx := &uploadContext{
		Server:             server,
		DataType:           dataType,
		ExpectedGameUserID: result.UserID,
		UploadMethod:       harukiUtils.UploadMethodInherit,
		FailureStage:       "retrieve_" + string(dataType),
	}
	dispatchUploadAuditLog(apiHelper, sharedDataHandlerLogger, uploadCtx, false, buildUploadAuditErrorMessage(err, nil))
}

func inheritRetrievalFailureDataType(uploadType harukiUtils.UploadDataType, err error) harukiUtils.UploadDataType {
	var retrievalErr *harukiSekai.DataRetrievalError
	if errors.As(err, &retrievalErr) {
		switch retrievalErr.DataType {
		case string(harukiUtils.UploadDataTypeMysekai):
			return harukiUtils.UploadDataTypeMysekai
		case string(harukiUtils.UploadDataTypeSuite):
			return harukiUtils.UploadDataTypeSuite
		}
	}
	if uploadType == harukiUtils.UploadDataTypeMysekai {
		return harukiUtils.UploadDataTypeMysekai
	}
	return harukiUtils.UploadDataTypeSuite
}

func uploadMysekaiDataIfNeeded(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, uploadType harukiUtils.UploadDataType, result *harukiUtils.SekaiInheritDataRetrieverResponse, server harukiUtils.SupportedDataUploadServer) error {
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
		server,
		harukiUtils.UploadDataTypeMysekai,
		&result.UserID,
		nil,
		apiHelper,
		harukiUtils.UploadMethodInherit,
	)
	if err != nil {
		if mapped := mapUploadProcessingError(err); mapped != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, mapped.Code, mapped.Message, nil)
		}
		return harukiAPIHelper.ErrorBadRequest(c, "failed to process mysekai upload")
	}
	return nil
}

func uploadSuiteData(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, result *harukiUtils.SekaiInheritDataRetrieverResponse, server harukiUtils.SupportedDataUploadServer) error {
	ctx := c.Context()
	if result.Suite == nil {
		return harukiAPIHelper.ErrorBadRequest(c, "Retrieve suite data failed: unknown error")
	}
	_, err := HandleUpload(
		ctx,
		result.Suite,
		server,
		harukiUtils.UploadDataTypeSuite,
		&result.UserID,
		nil,
		apiHelper,
		harukiUtils.UploadMethodInherit,
	)
	if err != nil {
		if mapped := mapUploadProcessingError(err); mapped != nil {
			return harukiAPIHelper.UpdatedDataResponse[string](c, mapped.Code, mapped.Message, nil)
		}
		return harukiAPIHelper.ErrorBadRequest(c, "failed to process suite upload")
	}
	return nil
}

func registerInheritRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	api := apiHelper.Router.Group("/api/inherit/:server/:upload_type", openUploadEntryGuard(apiHelper))

	api.Post("/submit", handleInheritSubmit(apiHelper))
}

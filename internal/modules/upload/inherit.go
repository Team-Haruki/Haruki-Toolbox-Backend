package upload

import (
	"errors"
	"fmt"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/sekai"
	"strconv"

	"github.com/gofiber/fiber/v3"
)

func handleInheritSubmit(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies) fiber.Handler {
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
		// Fast-fail when the game API for this server is already degraded, so we stop
		// hammering a failing upstream and the caller learns immediately instead of
		// blocking for the full ~90s run cap.
		allowed, retryAfter, breakerToken := inheritBreaker.Allow(server)
		if !allowed {
			c.Set("Retry-After", strconv.Itoa(retryAfterSeconds(retryAfter)))
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusServiceUnavailable, "game server temporarily degraded, please retry later", nil)
		}
		// Bound concurrent inherits per server; overflow fast-fails rather than
		// piling up slow goroutines against one game server. Release the breaker probe
		// permit (if this admission was a half-open probe) since we never touched the
		// upstream.
		if !inheritLimiter.acquire(server) {
			inheritBreaker.ReleaseProbe(server, breakerToken)
			c.Set("Retry-After", strconv.Itoa(retryAfterSeconds(inheritBreakerRetryAfterFloor)))
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusTooManyRequests, "too many concurrent inherit requests, please retry later", nil)
		}
		defer inheritLimiter.release(server)
		// Guarantee the breaker epoch is resolved even if retriever.Run panics (which
		// would otherwise leak a half-open probe). Default to degraded; the normal path
		// below overwrites it with the real verdict and marks it recorded. RecordResult
		// ignores a stale token, so the deferred call is a harmless no-op afterwards.
		breakerRecorded := false
		defer func() {
			if !breakerRecorded {
				inheritBreaker.RecordResult(server, breakerToken, true)
			}
		}()
		retriever := harukiSekai.NewSekaiDataRetriever(server, *data, uploadType, dependencies.ServerCryptor)
		result, err := retriever.Run(ctx)
		inheritBreaker.RecordResult(server, breakerToken, inheritFailureIsUpstreamDegradation(err))
		breakerRecorded = true
		if err != nil {
			uploadServer := harukiUtils.SupportedDataUploadServer(server)
			recordInheritRetrievalFailure(apiHelper, dependencies, uploadServer, uploadType, result, err)
			return harukiAPIHelper.ErrorBadRequest(c, "failed to retrieve game data")
		}
		uploadServer := harukiUtils.SupportedDataUploadServer(server)
		if err := uploadMysekaiDataIfNeeded(c, apiHelper, dependencies, uploadType, result, uploadServer); err != nil {
			return err
		}
		if err := uploadSuiteData(c, apiHelper, dependencies, result, uploadServer); err != nil {
			return err
		}
		return harukiAPIHelper.SuccessResponse[string](c, fmt.Sprintf("%s server user %d successfully uploaded data.", serverStr, result.UserID), nil)
	}
}

func recordInheritRetrievalFailure(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies, server harukiUtils.SupportedDataUploadServer, uploadType harukiUtils.UploadDataType, result *harukiUtils.SekaiInheritDataRetrieverResponse, err error) {
	logger := dependencies.DataHandlerLogger
	if result == nil || result.UserID <= 0 {
		logger.Warnf("Skip inherit retrieval failure upload log because game user ID is unavailable: %v", err)
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
	dispatchUploadAuditLog(apiHelper, logger, dependencies.BackgroundTasks, uploadCtx, false, buildUploadAuditErrorMessage(err, nil))
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

func uploadMysekaiDataIfNeeded(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies, uploadType harukiUtils.UploadDataType, result *harukiUtils.SekaiInheritDataRetrieverResponse, server harukiUtils.SupportedDataUploadServer) error {
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
		dependencies,
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

func uploadSuiteData(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies, result *harukiUtils.SekaiInheritDataRetrieverResponse, server harukiUtils.SupportedDataUploadServer) error {
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
		dependencies,
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

func registerInheritRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, dependencies Dependencies) {
	api := apiHelper.Router.Group("/api/inherit/:server/:upload_type", openUploadEntryGuard(apiHelper))

	api.Post("/submit", handleInheritSubmit(apiHelper, dependencies))
}

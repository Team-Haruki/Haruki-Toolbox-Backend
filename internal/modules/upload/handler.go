package upload

import (
	"context"
	"fmt"
	harukiConfig "haruki-suite/config"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	harukiDataHandler "haruki-suite/utils/handler"
	harukiHttp "haruki-suite/utils/http"
	harukiLogger "haruki-suite/utils/logger"
	"haruki-suite/utils/sekai"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
)

var (
	sharedHttpClient        *harukiHttp.Client
	sharedHttpClientProxy   string
	sharedHttpClientMu      sync.RWMutex
	uploadSemaphore         = make(chan struct{}, 10)
	uploadAuditSemaphore    = make(chan struct{}, 64)
	sharedDataHandlerLogger = harukiLogger.NewLoggerFromGlobal("SekaiDataHandler")
)

const (
	uploadStageBuildContext     = "build_context"
	uploadStageAccountPolicy    = "account_policy"
	uploadStageDecodePayload    = "decode_payload"
	uploadStageValidateIdentity = "validate_payload_identity"
	uploadStagePreprocess       = "preprocess"
	uploadStagePersist          = "persist"
	uploadStageValidateResult   = "validate_result"
)

func buildUploadAuditErrorMessage(err error, result *harukiUtils.HandleDataResult) *string {
	if result != nil {
		parts := make([]string, 0, 2)
		if result.Status != nil {
			parts = append(parts, fmt.Sprintf("status=%d", *result.Status))
		}
		if result.ErrorMessage != nil {
			trimmed := strings.TrimSpace(*result.ErrorMessage)
			if trimmed != "" {
				parts = append(parts, trimmed)
			}
		}
		if len(parts) > 0 {
			message := strings.Join(parts, " ")
			return &message
		}
	}
	if err == nil {
		return nil
	}
	trimmed := strings.TrimSpace(err.Error())
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func newUploadDataHandler(helper *harukiAPIHelper.HarukiToolboxRouterHelpers) *harukiDataHandler.DataHandler {
	return &harukiDataHandler.DataHandler{
		DBManager:      helper.DBManager,
		SekaiAPIClient: helper.SekaiAPIClient,
		HttpClient:     getSharedHTTPClient(),
		Logger:         sharedDataHandlerLogger,
		WebhookEnabled: helper.GetWebhookEnabled(),
	}
}

func getSharedHTTPClient() *harukiHttp.Client {
	proxy := strings.TrimSpace(harukiConfig.Cfg.Proxy)

	sharedHttpClientMu.RLock()
	if sharedHttpClient != nil && sharedHttpClientProxy == proxy {
		client := sharedHttpClient
		sharedHttpClientMu.RUnlock()
		return client
	}
	sharedHttpClientMu.RUnlock()

	client := harukiHttp.NewClient(proxy, 15*time.Second)

	sharedHttpClientMu.Lock()
	defer sharedHttpClientMu.Unlock()
	if sharedHttpClient == nil || sharedHttpClientProxy != proxy {
		sharedHttpClient = client
		sharedHttpClientProxy = proxy
	}
	return sharedHttpClient
}

func HandleUpload(
	ctx context.Context,
	data []byte,
	server harukiUtils.SupportedDataUploadServer,
	dataType harukiUtils.UploadDataType,
	gameUserID *int64,
	userID *string,
	helper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	uploadMethod harukiUtils.UploadMethod,
) (*harukiUtils.HandleDataResult, error) {

	uploadSemaphore <- struct{}{}
	defer func() { <-uploadSemaphore }()

	uploadCtx, err := buildUploadContext(server, dataType, gameUserID, userID, uploadMethod)
	if err != nil {
		return nil, err
	}
	handler := newUploadDataHandler(helper)
	auditWritten := false
	writeUploadAudit := func(success bool, errorMessage *string) {
		if auditWritten {
			return
		}
		dispatchUploadAuditLog(helper, handler.Logger, uploadCtx, success, errorMessage)
		auditWritten = true
	}
	fail := func(stage string, result *harukiUtils.HandleDataResult, err error) (*harukiUtils.HandleDataResult, error) {
		uploadCtx.FailureStage = stage
		if err != nil && handler.Logger != nil {
			handler.Logger.Warnf(
				"Upload failed stage=%s method=%s server=%s dataType=%s expectedGameUserId=%s parsedGameUserId=%s parsedGameUserIdType=%s err=%v",
				stage,
				uploadCtx.UploadMethod,
				uploadCtx.Server,
				uploadCtx.DataType,
				uploadCtx.expectedGameUserIDString(),
				uploadCtx.parsedGameUserIDString(),
				uploadCtx.ParsedGameUserIDType,
				err,
			)
		}
		writeUploadAudit(false, buildUploadAuditErrorMessage(err, result))
		return result, err
	}

	exists, belongs, settings, allowCNMySekai, userBanned, banReason, err := ParseGameAccountSetting(ctx, helper.DBManager.DB, string(uploadCtx.Server), uploadCtx.expectedGameUserIDString(), uploadCtx.UploadMethod, userID)
	if err != nil {
		return fail(uploadStageAccountPolicy, nil, err)
	}
	uploadCtx.Settings = settings
	if userBanned != nil && *userBanned {
		banMessage := "account owner is banned"
		if banReason != nil && *banReason != "" {
			banMessage = "account owner is banned: " + *banReason
		}
		err = fmt.Errorf("%w: %s", errUploadOwnerBanned, banMessage)
		return fail(uploadStageAccountPolicy, nil, err)
	}
	if err := validateGameAccountBelonging(belongs); err != nil {
		return fail(uploadStageAccountPolicy, nil, err)
	}
	uploadCtx.AllowPublicAPI = determinePublicAPIPermission(exists, uploadCtx.DataType, uploadCtx.Settings)
	if err := validateCNMysekaiAccess(uploadCtx.DataType, uploadCtx.Server, allowCNMySekai); err != nil {
		return fail(uploadStageAccountPolicy, nil, err)
	}

	unpackedMap, result, err := handler.DecodeUploadData(data, uploadCtx.Server)
	if err != nil {
		return fail(uploadStageDecodePayload, result, err)
	}
	parsedUserID, err := handler.ExtractGameUserID(unpackedMap)
	uploadCtx.ParsedGameUserID = parsedUserID.Value
	uploadCtx.ParsedGameUserIDType = parsedUserID.RawType
	if err != nil {
		return fail(uploadStageValidateIdentity, nil, err)
	}
	processedData, err := handler.PreHandleData(unpackedMap, &uploadCtx.ExpectedGameUserID, uploadCtx.ParsedGameUserID, uploadCtx.Server, uploadCtx.DataType)
	if err != nil {
		return fail(uploadStagePreprocess, nil, err)
	}
	if err := handler.PersistUploadData(ctx, processedData, uploadCtx.Server, uploadCtx.DataType, &uploadCtx.ExpectedGameUserID); err != nil {
		return fail(uploadStagePersist, nil, err)
	}
	result = &harukiUtils.HandleDataResult{UserID: &uploadCtx.ExpectedGameUserID}
	if err := validateUploadResult(result); err != nil {
		return fail(uploadStageValidateResult, result, err)
	}
	writeUploadAudit(true, nil)
	if err = helper.DBManager.Redis.ClearCache(ctx, string(uploadCtx.DataType), string(uploadCtx.Server), uploadCtx.ExpectedGameUserID); err != nil {
		handler.Logger.Warnf("Failed to clear redis cache: %v", err)
	}
	handler.RunUploadFanout(data, processedData, uploadCtx.Server, uploadCtx.DataType, &uploadCtx.ExpectedGameUserID, uploadCtx.Settings, uploadCtx.AllowPublicAPI)
	return result, nil
}

func dispatchUploadAuditLog(
	helper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	logger *harukiLogger.Logger,
	uploadCtx *uploadContext,
	success bool,
	errorMessage *string,
) {
	select {
	case uploadAuditSemaphore <- struct{}{}:
		go func() {
			defer func() { <-uploadAuditSemaphore }()
			persistUploadAuditLog(helper, logger, uploadCtx, success, errorMessage)
		}()
	default:
		persistUploadAuditLog(helper, logger, uploadCtx, success, errorMessage)
	}
}

func persistUploadAuditLog(
	helper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	logger *harukiLogger.Logger,
	uploadCtx *uploadContext,
	success bool,
	errorMessage *string,
) {
	if helper == nil || helper.DBManager == nil || helper.DBManager.DB == nil {
		if logger != nil {
			logger.Warnf("Skip upload audit log because DB helper is unavailable")
		}
		return
	}

	logCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	create := helper.DBManager.DB.UploadLog.Create().
		SetServer(string(uploadCtx.Server)).
		SetGameUserID(uploadCtx.expectedGameUserIDString()).
		SetToolboxUserID(uploadCtx.ToolboxUserID).
		SetDataType(string(uploadCtx.DataType)).
		SetUploadMethod(string(uploadCtx.UploadMethod)).
		SetSuccess(success).
		SetUploadTime(time.Now())
	if errorMessage != nil {
		create.SetErrorMessage(*errorMessage)
	}
	_, logErr := create.Save(logCtx)
	if logErr != nil {
		if logger != nil {
			logger.Warnf("Failed to create upload log: %v", logErr)
		}
	}

	targetType := "game_account"
	targetID := fmt.Sprintf("%s:%d", uploadCtx.Server, uploadCtx.ExpectedGameUserID)
	action := "user.upload." + strings.ToLower(string(uploadCtx.UploadMethod))
	actorType := harukiAPIHelper.SystemLogActorTypeSystem
	var actorUserID *string
	if strings.TrimSpace(uploadCtx.ToolboxUserID) != "" {
		actorType = harukiAPIHelper.SystemLogActorTypeUser
		userIDCopy := uploadCtx.ToolboxUserID
		actorUserID = &userIDCopy
	}

	systemLogErr := harukiAPIHelper.WriteSystemLog(logCtx, helper, harukiAPIHelper.SystemLogEntry{
		ActorUserID: actorUserID,
		ActorType:   actorType,
		Action:      action,
		TargetType:  &targetType,
		TargetID:    &targetID,
		Result: map[bool]string{
			true:  harukiAPIHelper.SystemLogResultSuccess,
			false: harukiAPIHelper.SystemLogResultFailure,
		}[success],
		Metadata: map[string]any{
			"server":               string(uploadCtx.Server),
			"gameUserId":           uploadCtx.expectedGameUserIDString(),
			"expectedGameUserId":   uploadCtx.expectedGameUserIDString(),
			"parsedGameUserId":     uploadCtx.parsedGameUserIDString(),
			"parsedGameUserIdType": uploadCtx.ParsedGameUserIDType,
			"dataType":             string(uploadCtx.DataType),
			"uploadMethod":         string(uploadCtx.UploadMethod),
			"failureStage":         uploadCtx.FailureStage,
			"errorMessage": func() string {
				if errorMessage == nil {
					return ""
				}
				return *errorMessage
			}(),
		},
	})
	if systemLogErr != nil && logger != nil {
		logger.Warnf("Failed to create system log: %v", systemLogErr)
	}
}

func applyProxyResponseHeaders(c fiber.Ctx, headers map[string][]string) {
	for key, values := range headers {
		if len(values) == 0 {
			continue
		}
		c.Set(key, values[0])
		if len(values) > 1 {
			for _, value := range values[1:] {
				c.Append(key, value)
			}
		}
	}
}

func HandleProxyUpload(
	proxy string,
	dataType harukiUtils.UploadDataType,
	helper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	mysekaiBirthdayPartyID *int64,
) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		serverStr := c.Params("server")
		userIDStr := c.Params("user_id")
		if userIDStr == "" {
			return fiber.NewError(fiber.StatusBadRequest, "invalid user_id")
		}
		userID, err := strconv.ParseInt(userIDStr, 10, 64)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid user_id format")
		}
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid server")
		}
		if dataType == harukiUtils.UploadDataTypeMysekaiBirthdayParty &&
			(mysekaiBirthdayPartyID == nil || *mysekaiBirthdayPartyID == 0) {
			return fiber.NewError(fiber.StatusBadRequest, "invalid birthday party_id")
		}
		headers := make(map[string]string)
		for k, v := range c.Request().Header.All() {
			headers[string(append([]byte(nil), k...))] = string(append([]byte(nil), v...))
		}
		var body []byte
		if c.Method() == fiber.MethodPost || c.Method() == fiber.MethodPut || c.Method() == fiber.MethodPatch {
			body = c.Body()
		}
		params := c.Queries()
		resp, err := sekai.HarukiSekaiProxyCallAPI(
			ctx,
			headers,
			c.Method(),
			server,
			dataType,
			body,
			params,
			proxy,
			userID,
			mysekaiBirthdayPartyID,
		)
		if err != nil {
			harukiLogger.Warnf("Proxy upload upstream call failed for %s/%s: %v", serverStr, dataType, err)
			return fiber.NewError(fiber.StatusInternalServerError, "proxy upstream request failed")
		}
		if dataType == harukiUtils.UploadDataTypeMysekaiBirthdayParty {
			unpackedData, err := sekai.Unpack(resp.RawBody, server)
			if err != nil {
				harukiLogger.Warnf("Proxy upload unpack failed for %s/%s/%s: %v", serverStr, userIDStr, dataType, err)
				return fiber.NewError(fiber.StatusInternalServerError, "failed to parse proxy response")
			}
			dataMap, ok := unpackedData.(map[string]any)
			if !ok {
				return fiber.NewError(fiber.StatusInternalServerError, "invalid response data format")
			}
			isRefreshed, ok := dataMap["isRefreshed"].(bool)
			if !ok || !isRefreshed {
				applyProxyResponseHeaders(c, resp.NewHeaders)
				return c.Status(resp.StatusCode).Send(resp.RawBody)
			}
		}
		if _, err := HandleUpload(ctx, resp.RawBody, server, dataType, &userID, nil, helper, harukiUtils.UploadMethodIOSProxy); err != nil {
			if mapped := mapUploadProcessingError(err); mapped != nil {
				return harukiAPIHelper.UpdatedDataResponse[string](c, mapped.Code, mapped.Message, nil)
			}
			harukiLogger.Warnf("Proxy upload persist failed for %s/%s/%s: %v", serverStr, userIDStr, dataType, err)
			return fiber.NewError(fiber.StatusInternalServerError, "failed to process uploaded data")
		}
		applyProxyResponseHeaders(c, resp.NewHeaders)
		return c.Status(resp.StatusCode).Send(resp.RawBody)
	}
}

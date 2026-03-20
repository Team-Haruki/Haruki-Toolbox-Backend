package upload

import (
	"context"
	"errors"
	"fmt"
	harukiConfig "haruki-suite/config"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	harukiDataHandler "haruki-suite/utils/handler"
	harukiHttp "haruki-suite/utils/http"
	harukiLogger "haruki-suite/utils/logger"
	"haruki-suite/utils/sekai"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
)

var (
	userIDSuffixRegex          = regexp.MustCompile(`user/(\d+)`)
	sharedHttpClient           *harukiHttp.Client
	sharedHttpClientProxy      string
	sharedHttpClientMu         sync.RWMutex
	uploadSemaphore            = make(chan struct{}, 10)
	uploadAuditSemaphore       = make(chan struct{}, 64)
	sharedDataHandlerLogger    = harukiLogger.NewLoggerFromGlobal("SekaiDataHandler")
	errUploadOwnershipMismatch = errors.New("upload game account ownership mismatch")
	errUploadOwnerBanned       = errors.New("upload game account owner banned")
	errUploadCNMysekaiDenied   = errors.New("upload cn mysekai denied")
)

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

func ExtractUploadTypeAndUserID(originalURL string) (harukiUtils.UploadDataType, int64) {
	if strings.Contains(originalURL, string(harukiUtils.UploadDataTypeSuite)) {
		match := userIDSuffixRegex.FindStringSubmatch(originalURL)
		if len(match) < 2 {
			return "", 0
		}
		userID, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return "", 0
		}
		return harukiUtils.UploadDataTypeSuite, userID
	} else if strings.Contains(originalURL, "birthday-party") && strings.Contains(originalURL, string(harukiUtils.UploadDataTypeMysekai)) {
		match := userIDSuffixRegex.FindStringSubmatch(originalURL)
		if len(match) < 2 {
			return "", 0
		}
		userID, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return "", 0
		}
		return harukiUtils.UploadDataTypeMysekaiBirthdayParty, userID
	} else if strings.Contains(originalURL, string(harukiUtils.UploadDataTypeMysekai)) {
		match := userIDSuffixRegex.FindStringSubmatch(originalURL)
		if len(match) < 2 {
			return "", 0
		}
		userID, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return "", 0
		}
		return harukiUtils.UploadDataTypeMysekai, userID
	}
	return "", 0
}

func ParseGameAccountSetting(ctx context.Context, db *postgresql.Client, server string, gameUserID string, uploadMethod harukiUtils.UploadMethod, userID *string) (bool, *bool, harukiAPIHelper.HarukiToolboxGameAccountPrivacySettings, *bool, *bool, *string, error) {
	var settings harukiAPIHelper.HarukiToolboxGameAccountPrivacySettings
	record, err := db.GameAccountBinding.
		Query().
		Where(
			gameaccountbinding.ServerEQ(server),
			gameaccountbinding.GameUserIDEQ(gameUserID),
		).
		WithUser().
		Only(ctx)
	if err != nil {
		if postgresql.IsNotFound(err) {
			return false, nil, settings, nil, nil, nil, nil
		}
		return false, nil, settings, nil, nil, nil, err
	}
	var belongs *bool
	var allowCNMysekai *bool
	var userBanned *bool
	var banReason *string
	if record.Edges.User != nil {
		ownerID := strings.TrimSpace(record.Edges.User.ID)
		a := record.Edges.User.AllowCnMysekai
		allowCNMysekai = &a
		banned := record.Edges.User.Banned
		userBanned = &banned
		banReason = record.Edges.User.BanReason
		belongs = deriveUploadOwnership(ownerID, userID, uploadMethod)
	}
	settings = harukiAPIHelper.HarukiToolboxGameAccountPrivacySettings{
		Suite:   record.Suite,
		Mysekai: record.Mysekai,
	}
	return true, belongs, settings, allowCNMysekai, userBanned, banReason, nil
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

	if _, err := harukiUtils.ParseSupportedDataUploadServer(string(server)); err != nil {
		return nil, fmt.Errorf("invalid server in HandleUpload: %w", err)
	}
	if _, err := harukiUtils.ParseUploadDataType(string(dataType)); err != nil {
		return nil, fmt.Errorf("invalid data_type in HandleUpload: %w", err)
	}
	handler := &harukiDataHandler.DataHandler{
		DBManager:      helper.DBManager,
		SekaiAPIClient: helper.SekaiAPIClient,
		HttpClient:     getSharedHTTPClient(),
		Logger:         sharedDataHandlerLogger,
	}
	exists, belongs, settings, allowCNMySekai, userBanned, banReason, err := ParseGameAccountSetting(ctx, helper.DBManager.DB, string(server), strconv.FormatInt(*gameUserID, 10), uploadMethod, userID)
	if err != nil {
		return nil, err
	}
	if userBanned != nil && *userBanned {
		banMessage := "account owner is banned"
		if banReason != nil && *banReason != "" {
			banMessage = "account owner is banned: " + *banReason
		}
		return nil, fmt.Errorf("%w: %s", errUploadOwnerBanned, banMessage)
	}
	if err := validateGameAccountBelonging(belongs); err != nil {
		return nil, err
	}
	allowPublicAPI := determinePublicAPIPermission(exists, dataType, settings)
	if err := validateCNMysekaiAccess(dataType, server, allowCNMySekai); err != nil {
		return nil, err
	}
	result, err := handler.HandleAndUpdateData(ctx, data, server, allowPublicAPI, dataType, gameUserID, settings)
	success := err == nil
	if err == nil {
		if vErr := validateUploadResult(result); vErr != nil {
			success = false
			err = vErr
		}
	}
	toolboxUserID := ""
	if userID != nil {
		toolboxUserID = *userID
	}
	dispatchUploadAuditLog(helper, handler.Logger, server, *gameUserID, toolboxUserID, dataType, uploadMethod, success)
	if err != nil {
		return result, err
	}
	if err = helper.DBManager.Redis.ClearCache(ctx, string(dataType), string(server), *gameUserID); err != nil {
		handler.Logger.Warnf("Failed to clear redis cache: %v", err)
	}
	return result, nil
}

func validateGameAccountBelonging(belongs *bool) error {
	if belongs != nil && !*belongs {
		return errUploadOwnershipMismatch
	}
	return nil
}

func determinePublicAPIPermission(exists bool, dataType harukiUtils.UploadDataType, settings harukiAPIHelper.HarukiToolboxGameAccountPrivacySettings) bool {
	if !exists {
		return false
	}
	if dataType == harukiUtils.UploadDataTypeMysekai {
		if settings.Mysekai != nil {
			return settings.Mysekai.AllowPublicApi
		}
		return false
	}
	if settings.Suite != nil {
		return settings.Suite.AllowPublicApi
	}
	return false
}

func validateCNMysekaiAccess(dataType harukiUtils.UploadDataType, server harukiUtils.SupportedDataUploadServer, allowCNMySekai *bool) error {
	if server == harukiUtils.SupportedDataUploadServerCN &&
		(dataType == harukiUtils.UploadDataTypeMysekai || dataType == harukiUtils.UploadDataTypeMysekaiBirthdayParty) {
		if allowCNMySekai != nil && !*allowCNMySekai {
			return errUploadCNMysekaiDenied
		}
	}
	return nil
}

func deriveUploadOwnership(ownerUserID string, currentUserID *string, uploadMethod harukiUtils.UploadMethod) *bool {
	ownerUserID = strings.TrimSpace(ownerUserID)
	if ownerUserID == "" {
		return nil
	}
	if currentUserID == nil {
		if allowAnonymousBoundAccountUpload(uploadMethod) {
			return nil
		}
		owned := false
		return &owned
	}
	owned := strings.TrimSpace(*currentUserID) == ownerUserID
	return &owned
}

func allowAnonymousBoundAccountUpload(uploadMethod harukiUtils.UploadMethod) bool {
	switch uploadMethod {
	case harukiUtils.UploadMethodIOSProxy, harukiUtils.UploadMethodInherit, harukiUtils.UploadMethodHarukiProxy:
		return true
	default:
		return false
	}
}

func mapUploadProcessingError(err error) *fiber.Error {
	switch {
	case errors.Is(err, errUploadOwnershipMismatch):
		return fiber.NewError(fiber.StatusForbidden, "upload is not allowed for this bound account")
	case errors.Is(err, errUploadOwnerBanned):
		return fiber.NewError(fiber.StatusForbidden, "account owner is banned")
	case errors.Is(err, errUploadCNMysekaiDenied):
		return fiber.NewError(fiber.StatusForbidden, "cn mysekai upload is not allowed")
	default:
		return nil
	}
}

func validateUploadResult(result *harukiUtils.HandleDataResult) error {
	if result.Status != nil && *result.Status != 200 {
		return errors.New("upload failed with status: " + strconv.Itoa(*result.Status))
	}
	return nil
}

func dispatchUploadAuditLog(
	helper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	logger *harukiLogger.Logger,
	server harukiUtils.SupportedDataUploadServer,
	gameUserID int64,
	toolboxUserID string,
	dataType harukiUtils.UploadDataType,
	uploadMethod harukiUtils.UploadMethod,
	success bool,
) {
	select {
	case uploadAuditSemaphore <- struct{}{}:
		go func() {
			defer func() { <-uploadAuditSemaphore }()
			persistUploadAuditLog(helper, logger, server, gameUserID, toolboxUserID, dataType, uploadMethod, success)
		}()
	default:
		persistUploadAuditLog(helper, logger, server, gameUserID, toolboxUserID, dataType, uploadMethod, success)
	}
}

func persistUploadAuditLog(
	helper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	logger *harukiLogger.Logger,
	server harukiUtils.SupportedDataUploadServer,
	gameUserID int64,
	toolboxUserID string,
	dataType harukiUtils.UploadDataType,
	uploadMethod harukiUtils.UploadMethod,
	success bool,
) {
	if helper == nil || helper.DBManager == nil || helper.DBManager.DB == nil {
		if logger != nil {
			logger.Warnf("Skip upload audit log because DB helper is unavailable")
		}
		return
	}

	logCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, logErr := helper.DBManager.DB.UploadLog.Create().
		SetServer(string(server)).
		SetGameUserID(strconv.FormatInt(gameUserID, 10)).
		SetToolboxUserID(toolboxUserID).
		SetDataType(string(dataType)).
		SetUploadMethod(string(uploadMethod)).
		SetSuccess(success).
		SetUploadTime(time.Now()).
		Save(logCtx)
	if logErr != nil {
		if logger != nil {
			logger.Warnf("Failed to create upload log: %v", logErr)
		}
	}

	targetType := "game_account"
	targetID := fmt.Sprintf("%s:%d", server, gameUserID)
	action := "user.upload." + strings.ToLower(string(uploadMethod))
	actorType := harukiAPIHelper.SystemLogActorTypeSystem
	var actorUserID *string
	if strings.TrimSpace(toolboxUserID) != "" {
		actorType = harukiAPIHelper.SystemLogActorTypeUser
		userIDCopy := toolboxUserID
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
			"server":       string(server),
			"gameUserId":   strconv.FormatInt(gameUserID, 10),
			"dataType":     string(dataType),
			"uploadMethod": string(uploadMethod),
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

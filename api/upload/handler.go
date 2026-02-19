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
	"time"

	"github.com/gofiber/fiber/v3"
)

var userIDSuffixRegex = regexp.MustCompile(`user/(\d+)`)

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

func ParseGameAccountSetting(ctx context.Context, db *postgresql.Client, server string, gameUserID string, userID *string) (bool, *bool, harukiAPIHelper.HarukiToolboxGameAccountPrivacySettings, *bool, *bool, *string, error) {
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
		a := record.Edges.User.AllowCnMysekai
		allowCNMysekai = &a
		banned := record.Edges.User.Banned
		userBanned = &banned
		banReason = record.Edges.User.BanReason
		if userID != nil {
			b := record.Edges.User.ID == *userID
			belongs = &b
		}
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
	if _, err := harukiUtils.ParseSupportedDataUploadServer(string(server)); err != nil {
		return nil, fmt.Errorf("invalid server in HandleUpload: %w", err)
	}
	if _, err := harukiUtils.ParseUploadDataType(string(dataType)); err != nil {
		return nil, fmt.Errorf("invalid data_type in HandleUpload: %w", err)
	}
	handler := &harukiDataHandler.DataHandler{
		DBManager:      helper.DBManager,
		SekaiAPIClient: helper.SekaiAPIClient,
		HttpClient:     harukiHttp.NewClient(harukiConfig.Cfg.Proxy, 15*time.Second),
		Logger:         harukiLogger.NewLogger("SekaiDataHandler", "DEBUG", nil),
	}
	exists, belongs, settings, allowCNMySekai, userBanned, banReason, err := ParseGameAccountSetting(ctx, helper.DBManager.DB, string(server), strconv.FormatInt(*gameUserID, 10), userID)
	if err != nil {
		return nil, err
	}
	if userBanned != nil && *userBanned {
		banMessage := "account owner is banned"
		if banReason != nil && *banReason != "" {
			banMessage = "account owner is banned: " + *banReason
		}
		return nil, errors.New(banMessage)
	}
	if err := validateGameAccountBelonging(belongs); err != nil {
		return nil, err
	}
	allowPublicAPI := determinePublicAPIPermission(exists, dataType, settings)
	if err := validateCNMysekaiAccess(dataType, server, userID, allowCNMySekai); err != nil {
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
	go func() {
		logCtx := context.Background()
		_, logErr := helper.DBManager.DB.UploadLog.Create().
			SetServer(string(server)).
			SetGameUserID(strconv.FormatInt(*gameUserID, 10)).
			SetToolboxUserID(toolboxUserID).
			SetDataType(string(dataType)).
			SetUploadMethod(string(uploadMethod)).
			SetSuccess(success).
			SetUploadTime(time.Now()).
			Save(logCtx)
		if logErr != nil {
			handler.Logger.Warnf("Failed to create upload log: %v", logErr)
		}
	}()
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
		return errors.New("game account does not belong to the user")
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

func validateCNMysekaiAccess(dataType harukiUtils.UploadDataType, server harukiUtils.SupportedDataUploadServer, userID *string, allowCNMySekai *bool) error {
	if dataType == harukiUtils.UploadDataTypeMysekai && server == harukiUtils.SupportedDataUploadServerCN {
		if userID != nil && allowCNMySekai != nil && !*allowCNMySekai {
			return errors.New("illegal request")
		}
	}
	return nil
}

func validateUploadResult(result *harukiUtils.HandleDataResult) error {
	if result.Status != nil && *result.Status != 200 {
		return errors.New("upload failed with status: " + strconv.Itoa(*result.Status))
	}
	return nil
}

func HandleProxyUpload(
	proxy string,
	dataType harukiUtils.UploadDataType,
	helper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	mysekaiBirthdayPartyID *int64,
) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := context.Background()
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
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
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
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		if dataType == harukiUtils.UploadDataTypeMysekaiBirthdayParty {
			unpackedData, err := sekai.Unpack(resp.RawBody, server)
			if err != nil {
				return fiber.NewError(fiber.StatusInternalServerError, err.Error())
			}
			dataMap, ok := unpackedData.(map[string]interface{})
			if !ok {
				return fiber.NewError(fiber.StatusInternalServerError, "invalid response data format")
			}
			isRefreshed, ok := dataMap["isRefreshed"].(bool)
			if !ok || !isRefreshed {
				for k, v := range resp.NewHeaders {
					c.Set(k, v)
				}
				return c.Status(resp.StatusCode).Send(resp.RawBody)
			}
		}
		if _, err := HandleUpload(ctx, resp.RawBody, server, dataType, &userID, nil, helper, harukiUtils.UploadMethodIOSProxy); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		for k, v := range resp.NewHeaders {
			c.Set(k, v)
		}
		return c.Status(resp.StatusCode).Send(resp.RawBody)
	}
}

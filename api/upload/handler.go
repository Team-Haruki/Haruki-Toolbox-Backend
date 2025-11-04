package upload

import (
	"context"
	"errors"
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

func ExtractUploadTypeAndUserID(originalURL string) (harukiUtils.UploadDataType, int64) {
	if strings.Contains(originalURL, string(harukiUtils.UploadDataTypeSuite)) {
		re := regexp.MustCompile(`user/(\d+)`)
		match := re.FindStringSubmatch(originalURL)

		if len(match) < 2 {
			return "", 0
		}

		userID, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return "", 0
		}

		return harukiUtils.UploadDataTypeSuite, userID

	} else if strings.Contains(originalURL, string(harukiUtils.UploadDataTypeMysekai)) {
		re := regexp.MustCompile(`user/(\d+)`)
		match := re.FindStringSubmatch(originalURL)

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

func ParseGameAccountSetting(ctx context.Context, db *postgresql.Client, server string, gameUserID string, userID *string) (bool, *bool, harukiAPIHelper.HarukiToolboxGameAccountPrivacySettings, *bool, error) {
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
			return false, nil, settings, nil, nil
		}
		return false, nil, settings, nil, err
	}

	var belongs *bool
	var allowCNMysekai *bool
	if userID != nil {
		a := record.Edges.User.AllowCnMysekai
		b := record.Edges.User.ID == *userID
		belongs = &b
		allowCNMysekai = &a
	}

	settings = harukiAPIHelper.HarukiToolboxGameAccountPrivacySettings{
		Suite:   record.Suite,
		Mysekai: record.Mysekai,
	}

	return true, belongs, settings, allowCNMysekai, nil
}

func HandleUpload(
	ctx context.Context,
	data []byte,
	server harukiUtils.SupportedDataUploadServer,
	dataType harukiUtils.UploadDataType,
	gameUserID *int64,
	userID *string,
	helper *harukiAPIHelper.HarukiToolboxRouterHelpers,
) (*harukiUtils.HandleDataResult, error) {

	handler := &harukiDataHandler.DataHandler{
		DBManager:      helper.DBManager,
		SeakiAPIClient: helper.SekaiAPIClient,
		HttpClient:     harukiHttp.NewClient(harukiConfig.Cfg.Proxy, 15*time.Second),
		Logger:         harukiLogger.NewLogger("SekaiDataHandler", "DEBUG", nil),
	}

	exists, belongs, settings, allowCNMySekai, err := ParseGameAccountSetting(ctx, helper.DBManager.DB, string(server), strconv.FormatInt(*gameUserID, 10), userID)
	if err != nil {
		return nil, err
	}

	if err := validateGameAccountBelonging(belongs); err != nil {
		return nil, err
	}

	allowPublicAPI := determinePublicAPIPermission(exists, dataType, settings)

	if err := validateCNMysekaiAccess(dataType, server, userID, allowCNMySekai); err != nil {
		return nil, err
	}

	result, err := handler.HandleAndUpdateData(ctx, data, server, allowPublicAPI, dataType, gameUserID, settings)
	if err != nil {
		return result, err
	}

	if err := validateUploadResult(result); err != nil {
		return result, err
	}

	if err = helper.DBManager.Redis.ClearCache(ctx, string(dataType), string(server), *gameUserID); err != nil {
		return result, err
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

		headers := make(map[string]string)
		for k, v := range c.Request().Header.All() {
			headers[string(append([]byte(nil), k...))] = string(append([]byte(nil), v...))
		}

		var body []byte
		if c.Method() == fiber.MethodPost {
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
		)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}

		if _, err := HandleUpload(ctx, resp.RawBody, server, dataType, &userID, nil, helper); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}

		for k, v := range resp.NewHeaders {
			c.Set(k, v)
		}
		return c.Status(resp.StatusCode).Send(resp.RawBody)
	}
}

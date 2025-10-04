package upload

import (
	"context"
	"errors"
	harukiConfig "haruki-suite/config"
	"haruki-suite/ent/schema"
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

	"github.com/gofiber/fiber/v2"
)

var logger = harukiLogger.NewLogger("HandlerDebugger", "DEBUG", nil)

type HarukiToolboxGameAccountPrivacySettings struct {
	Suite   *schema.SuiteDataPrivacySettings   `json:"suite"`
	Mysekai *schema.MysekaiDataPrivacySettings `json:"mysekai"`
}

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

func ParseGameAccountSetting(ctx context.Context, db *postgresql.Client, server string, gameUserID string, userID *string) (bool, *bool, HarukiToolboxGameAccountPrivacySettings, *bool, error) {
	var settings HarukiToolboxGameAccountPrivacySettings

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

	settings = HarukiToolboxGameAccountPrivacySettings{
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
	logger.Debugf("HandleUpload called: server=%s dataType=%s gameUserID=%v userID=%v", server, dataType, gameUserID, userID)

	handler := &harukiDataHandler.DataHandler{
		DBManager:      helper.DBManager,
		SeakiAPIClient: helper.SekaiAPIClient,
		HttpClient:     harukiHttp.NewClient(harukiConfig.Cfg.Proxy, 15*time.Second),
		Logger:         harukiLogger.NewLogger("SekaiDataHandler", "DEBUG", nil),
	}

	var allowPublicAPI bool
	exists, belongs, settings, allowCNMySekai, err := ParseGameAccountSetting(ctx, helper.DBManager.DB, string(server), strconv.FormatInt(*gameUserID, 10), userID)
	logger.Debugf("ParseGameAccountSetting result: exists=%v belongs=%v settings=%+v allowCNMySekai=%v err=%v", exists, belongs, settings, allowCNMySekai, err)
	if err != nil {
		return nil, err
	}
	if !exists {
		logger.Debugf("Game account does not exist")
		allowPublicAPI = false
	}
	if belongs != nil && !*belongs {
		logger.Debugf("Game account does not belong to the user")
		return nil, errors.New("game account does not belong to the user")
	}

	if dataType == harukiUtils.UploadDataTypeMysekai {
		if settings.Mysekai != nil {
			allowPublicAPI = settings.Mysekai.AllowPublicApi
		} else {
			allowPublicAPI = false
		}
	} else {
		if settings.Suite != nil {
			allowPublicAPI = settings.Suite.AllowPublicApi
		} else {
			allowPublicAPI = false
		}
	}

	if dataType == harukiUtils.UploadDataTypeMysekai && server == harukiUtils.SupportedDataUploadServerCN {
		if userID != nil {
			if allowCNMySekai != nil {
				if !*allowCNMySekai {
					return nil, errors.New("illegal request")
				}
			}
		}
	}

	logger.Debugf("About to call HandleAndUpdateData with allowPublicAPI=%v", allowPublicAPI)
	result, err := handler.HandleAndUpdateData(ctx, data, server, allowPublicAPI, dataType, gameUserID)
	if err != nil {
		return result, err
	}

	if result.Status != nil {
		logger.Debugf("HandleAndUpdateData returned status=%d", *result.Status)
		if *result.Status != 200 {
			return result, errors.New("upload failed with status: " + strconv.Itoa(*result.Status))
		}
	}

	logger.Debugf("Clearing cache for dataType=%s server=%s gameUserID=%d", dataType, server, *gameUserID)
	if err = helper.DBManager.Redis.ClearCache(ctx, string(dataType), string(server), *gameUserID); err != nil {
		return result, err
	}

	logger.Debugf("HandleUpload completed successfully")
	return result, nil
}

func HandleProxyUpload(
	proxy string,
	dataType harukiUtils.UploadDataType,
	helper *harukiAPIHelper.HarukiToolboxRouterHelpers,
) fiber.Handler {
	return func(c *fiber.Ctx) error {
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

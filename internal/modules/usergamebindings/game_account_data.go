package usergamebindings

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/api/data"
	harukiLogger "haruki-suite/utils/logger"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

type ownedGameAccountDataType string

const (
	ownedGameAccountDataTypeSuite   ownedGameAccountDataType = "suite"
	ownedGameAccountDataTypeMysekai ownedGameAccountDataType = "mysekai"
	ownedGameAccountDataTypeProfile ownedGameAccountDataType = "profile"
)

func parseOwnedGameAccountDataType(raw string) (ownedGameAccountDataType, *fiber.Error) {
	dataType := ownedGameAccountDataType(strings.ToLower(strings.TrimSpace(raw)))
	switch dataType {
	case ownedGameAccountDataTypeSuite, ownedGameAccountDataTypeMysekai, ownedGameAccountDataTypeProfile:
		return dataType, nil
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid data_type")
	}
}

func buildPublicAPIAllowedKeySet(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) (map[string]struct{}, []string) {
	allowedKeys := apiHelper.GetPublicAPIAllowedKeys()
	allowedKeySet := make(map[string]struct{}, len(allowedKeys))
	for _, key := range allowedKeys {
		allowedKeySet[key] = struct{}{}
	}
	return allowedKeySet, allowedKeys
}

func handleGetOwnedGameAccountData(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()

		authUserID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}

		serverStr := c.Params("server")
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverStr)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid server")
		}

		gameUserIDStr := strings.TrimSpace(c.Params("game_user_id"))
		gameUserID, err := strconv.ParseInt(gameUserIDStr, 10, 64)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "game_user_id must be numeric")
		}

		dataType, parseErr := parseOwnedGameAccountDataType(c.Params("data_type"))
		if parseErr != nil {
			return harukiAPIHelper.ErrorBadRequest(c, parseErr.Message)
		}

		access, err := apiHelper.DBManager.DB.CanAccessGameAccountData(ctx, authUserID, string(server), gameUserIDStr, string(dataType), time.Now().UTC())
		if err != nil {
			harukiLogger.Errorf("Failed to verify game account data access: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to verify game account data access")
		}
		if access == nil || !access.Allowed {
			if access == nil || access.OwnerUserID == "" {
				return harukiAPIHelper.ErrorNotFound(c, "binding not found")
			}
			return harukiAPIHelper.ErrorForbidden(c, "not authorized to access this binding")
		}

		switch dataType {
		case ownedGameAccountDataTypeSuite:
			resp, err := handleOwnedSuiteData(c, apiHelper, gameUserID, server)
			if err != nil {
				return respondVerifiedGameAccountDataError(c, err)
			}
			return c.JSON(resp)
		case ownedGameAccountDataTypeMysekai:
			resp, err := data.HandleMysekaiRequest(c, apiHelper, gameUserID, server, c.Query("key"))
			if err != nil {
				return respondVerifiedGameAccountDataError(c, err)
			}
			return c.JSON(resp)
		case ownedGameAccountDataTypeProfile:
			if access.ViaGrant {
				return harukiAPIHelper.ErrorForbidden(c, "profile access cannot be granted")
			}
			return sendOwnedGameAccountProfile(c, apiHelper, gameUserIDStr, server)
		default:
			return harukiAPIHelper.ErrorBadRequest(c, "invalid data_type")
		}
	}
}

func handleOwnedSuiteData(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, gameUserID int64, server harukiUtils.SupportedDataUploadServer) (any, error) {
	allowedKeySet, allowedKeys := buildPublicAPIAllowedKeySet(apiHelper)
	return data.HandleSuiteRequest(c, apiHelper, gameUserID, server, c.Query("key"), allowedKeySet, allowedKeys)
}

func sendOwnedGameAccountProfile(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, gameUserIDStr string, server harukiUtils.SupportedDataUploadServer) error {
	if apiHelper == nil || apiHelper.SekaiAPIClient == nil {
		return harukiAPIHelper.ErrorInternal(c, "profile service unavailable")
	}

	resultInfo, body, err := apiHelper.SekaiAPIClient.GetUserProfile(gameUserIDStr, string(server))
	if err != nil {
		if resultInfo != nil {
			if !resultInfo.ServerAvailable {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadGateway, "game server unavailable", nil)
			}
			if !resultInfo.AccountExists {
				return harukiAPIHelper.ErrorNotFound(c, "game account not found")
			}
		}
		harukiLogger.Errorf("Failed to query game account profile: %v", err)
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadGateway, "failed to query game account profile", nil)
	}
	if resultInfo == nil {
		harukiLogger.Errorf("Sekai API profile response missing result info")
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadGateway, "failed to query game account profile", nil)
	}
	if !resultInfo.ServerAvailable {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadGateway, "game server unavailable", nil)
	}
	if !resultInfo.AccountExists {
		return harukiAPIHelper.ErrorNotFound(c, "game account not found")
	}
	if !resultInfo.Body || len(body) == 0 {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusBadGateway, "empty game account profile response", nil)
	}

	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	return c.Send(body)
}

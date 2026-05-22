package usergamebindings

import (
	"errors"
	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	harukiAPIData "haruki-suite/utils/api/data"
	"haruki-suite/utils/database/postgresql"
	harukiLogger "haruki-suite/utils/logger"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
)

type deckRecommendDataMode string

const (
	deckRecommendDataModeSuite   deckRecommendDataMode = "suite"
	deckRecommendDataModeMysekai deckRecommendDataMode = "mysekai"
)

type deckRecommendDataResponse struct {
	Server     harukiUtils.SupportedDataUploadServer `json:"server"`
	GameUserID string                                `json:"gameUserId"`
	Mode       deckRecommendDataMode                 `json:"mode"`
	UserData   map[string]any                        `json:"userData"`
}

func parseDeckRecommendDataMode(raw string) (deckRecommendDataMode, *fiber.Error) {
	mode := deckRecommendDataMode(strings.ToLower(strings.TrimSpace(raw)))
	if mode == "" {
		return deckRecommendDataModeSuite, nil
	}
	switch mode {
	case deckRecommendDataModeSuite, deckRecommendDataModeMysekai:
		return mode, nil
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid mode")
	}
}

func validateVerifiedOwnedGameAccountBinding(binding *postgresql.GameAccountBinding, userID string) *fiber.Error {
	if binding == nil {
		return fiber.NewError(fiber.StatusNotFound, "binding not found")
	}
	if bindingOwnerMissing(binding) {
		return fiber.NewError(fiber.StatusConflict, "binding owner missing")
	}
	if !isBindingOwnedByUser(binding, userID) {
		return fiber.NewError(fiber.StatusForbidden, "not authorized to access this binding")
	}
	if !binding.Verified {
		return fiber.NewError(fiber.StatusBadRequest, "binding is not verified yet")
	}
	return nil
}

func handleGetDeckRecommendData(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID, err := userCoreModule.CurrentUserID(c)
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

		modeQuery := c.Query("mode")
		if modeQuery == "" {
			modeQuery = c.Query("data_type")
		}
		mode, modeErr := parseDeckRecommendDataMode(modeQuery)
		if modeErr != nil {
			return harukiAPIHelper.ErrorBadRequest(c, modeErr.Message)
		}

		binding, err := queryExistingBinding(ctx, apiHelper, serverStr, gameUserIDStr)
		if err != nil {
			harukiLogger.Errorf("Failed to query deck recommend binding: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to query binding")
		}
		if bindingErr := validateVerifiedOwnedGameAccountBinding(binding, userID); bindingErr != nil {
			return respondVerifiedGameAccountDataError(c, bindingErr)
		}

		userData, err := harukiAPIData.LoadDeckRecommendUserData(
			ctx,
			apiHelper,
			gameUserID,
			server,
			mode == deckRecommendDataModeMysekai,
		)
		if err != nil {
			return respondVerifiedGameAccountDataError(c, err)
		}

		resp := deckRecommendDataResponse{
			Server:     server,
			GameUserID: gameUserIDStr,
			Mode:       mode,
			UserData:   userData,
		}
		return harukiAPIHelper.SuccessResponse(c, "ok", &resp)
	}
}

func respondVerifiedGameAccountDataError(c fiber.Ctx, err error) error {
	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		switch fiberErr.Code {
		case fiber.StatusBadRequest:
			return harukiAPIHelper.ErrorBadRequest(c, fiberErr.Message)
		case fiber.StatusUnauthorized:
			return harukiAPIHelper.ErrorUnauthorized(c, fiberErr.Message)
		case fiber.StatusForbidden:
			return harukiAPIHelper.ErrorForbidden(c, fiberErr.Message)
		case fiber.StatusNotFound:
			return harukiAPIHelper.ErrorNotFound(c, fiberErr.Message)
		case fiber.StatusConflict:
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, fiberErr.Message, nil)
		default:
			return harukiAPIHelper.ErrorInternal(c, fiberErr.Message)
		}
	}
	return harukiAPIHelper.ErrorInternal(c, "failed to get game account data")
}

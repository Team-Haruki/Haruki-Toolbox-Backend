package usergamebindings

import (
	"context"
	"errors"
	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiAPIData "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api/data"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

// deckRecommendReadTimeout bounds the deck-recommend data read. It is wider than
// the other read deadlines because mysekai mode issues two sequential Mongo
// fetches (suite subset + mysekai subset).
const deckRecommendReadTimeout = 5 * time.Second

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

// deckRecommendRequiredDataTypes lists the game-account data types
// recommend-data reads for a mode. It is the single source of truth shared with
// the accessible-accounts read model (applyDerivedGameAccountCapabilities), so a
// client can never be offered a mode this handler would then refuse.
func deckRecommendRequiredDataTypes(mode deckRecommendDataMode) []ownedGameAccountDataType {
	if mode == deckRecommendDataModeMysekai {
		return []ownedGameAccountDataType{ownedGameAccountDataTypeSuite, ownedGameAccountDataTypeMysekai}
	}
	return []ownedGameAccountDataType{ownedGameAccountDataTypeSuite}
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
		// Bound the PG binding lookup and both Mongo fetches; Fiber v3 request
		// contexts carry no deadline.
		ctx, cancel := context.WithTimeout(c.Context(), deckRecommendReadTimeout)
		defer cancel()
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

		// Deck recommend reads the same per-account data as the generic
		// game-account endpoint, so it authorizes the same way: owned bindings
		// and live grants both qualify, one check per data type the mode needs.
		now := time.Now().UTC()
		for _, requiredDataType := range deckRecommendRequiredDataTypes(mode) {
			access, err := apiHelper.DBManager.DB.CanAccessGameAccountData(ctx, userID, string(server), gameUserIDStr, string(requiredDataType), now)
			if err != nil {
				harukiLogger.Errorf("Failed to verify deck recommend data access: %v", err)
				return harukiAPIHelper.ErrorInternal(c, "failed to verify game account data access")
			}
			if access == nil || !access.Allowed {
				if access == nil || access.OwnerUserID == "" {
					return harukiAPIHelper.ErrorNotFound(c, "binding not found")
				}
				return harukiAPIHelper.ErrorForbidden(c, "not authorized to access this binding")
			}
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

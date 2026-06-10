package usergamebindings

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"
	harukiLogger "haruki-suite/utils/logger"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

func parseGrantRouteParams(c fiber.Ctx) (harukiUtils.SupportedDataUploadServer, string, string, string, *fiber.Error) {
	server, err := harukiUtils.ParseSupportedDataUploadServer(c.Params("server"))
	if err != nil {
		return "", "", "", "", fiber.NewError(fiber.StatusBadRequest, "invalid server")
	}
	gameUserID := strings.TrimSpace(c.Params("game_user_id"))
	if _, err := strconv.ParseInt(gameUserID, 10, 64); err != nil {
		return "", "", "", "", fiber.NewError(fiber.StatusBadRequest, "game_user_id must be numeric")
	}
	dataType := strings.ToLower(strings.TrimSpace(c.Params("data_type")))
	if !postgresql.IsGrantableGameAccountDataType(dataType) {
		return "", "", "", "", fiber.NewError(fiber.StatusBadRequest, "invalid data_type")
	}
	granteeUserID := strings.TrimSpace(c.Params("grantee_user_id"))
	if granteeUserID == "" {
		return "", "", "", "", fiber.NewError(fiber.StatusBadRequest, "grantee_user_id is required")
	}
	return server, gameUserID, dataType, granteeUserID, nil
}

func validateGrantOwnerBinding(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, ownerUserID string, server harukiUtils.SupportedDataUploadServer, gameUserID string) error {
	binding, err := queryExistingBinding(c.Context(), apiHelper, string(server), gameUserID)
	if err != nil {
		harukiLogger.Errorf("Failed to query game account grant binding: %v", err)
		return harukiAPIHelper.ErrorInternal(c, "failed to query binding")
	}
	if bindingErr := validateVerifiedOwnedGameAccountBinding(binding, ownerUserID); bindingErr != nil {
		return respondVerifiedGameAccountDataError(c, bindingErr)
	}
	return nil
}

func validateGrantTargetUser(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, granteeUserID string) error {
	target, err := apiHelper.DBManager.DB.User.Query().
		Where(userSchema.IDEQ(granteeUserID)).
		Select(userSchema.FieldID, userSchema.FieldBanned, userSchema.FieldBanReason).
		Only(c.Context())
	if err != nil {
		if postgresql.IsNotFound(err) {
			return harukiAPIHelper.ErrorNotFound(c, "grantee user not found")
		}
		harukiLogger.Errorf("Failed to query grant target user: %v", err)
		return harukiAPIHelper.ErrorInternal(c, "failed to query grantee user")
	}
	if target.Banned {
		return harukiAPIHelper.ErrorForbidden(c, "grantee user is banned")
	}
	return nil
}

func handleListOwnedGameAccountDataGrants(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		now := gameAccountGrantNowUTC()
		records, err := apiHelper.DBManager.DB.ListOwnedGameAccountDataGrants(c.Context(), userID, now)
		if err != nil {
			harukiLogger.Errorf("Failed to list owned game account data grants: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to list game account data grants")
		}
		items := buildGameAccountDataGrantItems(records)
		resp := gameAccountDataGrantListResponse{
			GeneratedAt: now,
			Total:       len(items),
			Items:       items,
		}
		return harukiAPIHelper.SuccessResponse(c, "ok", &resp)
	}
}

func handleListReceivedGameAccountDataGrants(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		now := gameAccountGrantNowUTC()
		records, err := apiHelper.DBManager.DB.ListReceivedGameAccountDataGrants(c.Context(), userID, now)
		if err != nil {
			harukiLogger.Errorf("Failed to list received game account data grants: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to list received game account data grants")
		}
		items := buildGameAccountDataGrantItems(records)
		resp := gameAccountDataGrantListResponse{
			GeneratedAt: now,
			Total:       len(items),
			Items:       items,
		}
		return harukiAPIHelper.SuccessResponse(c, "ok", &resp)
	}
}

func handleUpsertGameAccountDataGrant(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ownerUserID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		server, gameUserID, dataType, granteeUserID, parseErr := parseGrantRouteParams(c)
		if parseErr != nil {
			return harukiAPIHelper.ErrorBadRequest(c, parseErr.Message)
		}
		if ownerUserID == granteeUserID {
			return harukiAPIHelper.ErrorBadRequest(c, "cannot grant access to yourself")
		}
		var payload gameAccountDataGrantPayload
		if err := c.Bind().Body(&payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		expiresAt := payload.ExpiresAt.UTC()
		if expiresAt.IsZero() || !expiresAt.After(time.Now().UTC()) {
			return harukiAPIHelper.ErrorBadRequest(c, "expiresAt must be a future time")
		}
		if err := validateGrantOwnerBinding(c, apiHelper, ownerUserID, server, gameUserID); err != nil {
			return err
		}
		if err := validateGrantTargetUser(c, apiHelper, granteeUserID); err != nil {
			return err
		}

		row, err := apiHelper.DBManager.DB.UpsertGameAccountDataGrant(c.Context(), ownerUserID, granteeUserID, string(server), gameUserID, dataType, expiresAt)
		if err != nil {
			harukiLogger.Errorf("Failed to upsert game account data grant: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to save game account data grant")
		}
		resp := gameAccountDataGrantMutationResponse{
			GeneratedAt: gameAccountGrantNowUTC(),
			Grant:       buildGameAccountDataGrantItemFromRow(row),
		}
		userCoreModule.WriteUserAuditLog(c, apiHelper, "user.game_account_data_grant.upsert", harukiAPIHelper.SystemLogResultSuccess, ownerUserID, map[string]any{
			"server":        string(server),
			"gameUserID":    gameUserID,
			"dataType":      dataType,
			"granteeUserID": granteeUserID,
			"expiresAt":     expiresAt.Format(time.RFC3339),
		})
		return harukiAPIHelper.SuccessResponse(c, "game account data grant saved", &resp)
	}
}

func handleDeleteGameAccountDataGrant(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ownerUserID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		server, gameUserID, dataType, granteeUserID, parseErr := parseGrantRouteParams(c)
		if parseErr != nil {
			return harukiAPIHelper.ErrorBadRequest(c, parseErr.Message)
		}
		affected, err := apiHelper.DBManager.DB.DeleteGameAccountDataGrant(c.Context(), ownerUserID, granteeUserID, string(server), gameUserID, dataType)
		if err != nil {
			harukiLogger.Errorf("Failed to delete game account data grant: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to delete game account data grant")
		}
		if affected == 0 {
			return harukiAPIHelper.ErrorNotFound(c, "game account data grant not found")
		}
		userCoreModule.WriteUserAuditLog(c, apiHelper, "user.game_account_data_grant.delete", harukiAPIHelper.SystemLogResultSuccess, ownerUserID, map[string]any{
			"server":        string(server),
			"gameUserID":    gameUserID,
			"dataType":      dataType,
			"granteeUserID": granteeUserID,
		})
		return harukiAPIHelper.SuccessResponse[string](c, "game account data grant deleted", nil)
	}
}

package usergamebindings

import (
	"errors"
	"strconv"
	"strings"
	"time"

	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountdatagrant"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
)

func parseGrantRouteParams(c fiber.Ctx) (harukiUtils.SupportedDataUploadServer, string, string, string, *fiber.Error) {
	server, err := harukiUtils.ParseSupportedDataUploadServer(c.Params("server"))
	if err != nil {
		return "", "", "", "", fiber.NewError(fiber.StatusBadRequest, "invalid server")
	}
	numericID, err := strconv.ParseInt(strings.TrimSpace(c.Params("game_user_id")), 10, 64)
	if err != nil {
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
	return server, strconv.FormatInt(numericID, 10), dataType, granteeUserID, nil
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
		return harukiAPIHelper.Responses.SuccessResponse(c, "ok", &resp)
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
		return harukiAPIHelper.Responses.SuccessResponse(c, "ok", &resp)
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
		release, err := apiHelper.DBManager.DB.LockGameAccountMutation(c.Context(), string(server), gameUserID)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to reserve grant update")
		}
		defer release()

		var permissions [][]string
		if payload.Permissions != nil {
			if _, _, err := postgresql.ParseGrantPermissions(dataType, *payload.Permissions); err != nil {
				return harukiAPIHelper.ErrorBadRequest(c, "invalid permissions")
			}
			permissions = append(permissions, *payload.Permissions)
		}
		previous, err := apiHelper.DBManager.DB.GameAccountDataGrant.Query().Where(gameaccountdatagrant.OwnerUserIDEQ(ownerUserID), gameaccountdatagrant.GranteeUserIDEQ(granteeUserID), gameaccountdatagrant.ServerEQ(string(server)), gameaccountdatagrant.GameUserIDEQ(gameUserID), gameaccountdatagrant.DataTypeEQ(dataType)).Only(c.Context())
		if err != nil && !postgresql.IsNotFound(err) {
			return harukiAPIHelper.ErrorInternal(c, "failed to query previous grant")
		}
		previousPermissions := []string{}
		if previous != nil {
			previousPermissions = postgresql.GrantPermissions(previous.CanRead, previous.CanWrite)
		}
		row, err := apiHelper.DBManager.DB.UpsertGameAccountDataGrant(c.Context(), ownerUserID, granteeUserID, string(server), gameUserID, dataType, expiresAt, permissions...)
		switch {
		case errors.Is(err, postgresql.ErrGameAccountDataGrantOwnerNotFound):
			return harukiAPIHelper.ErrorUnauthorized(c, "invalid user session")
		case errors.Is(err, postgresql.ErrGameAccountDataGrantOwnerBanned):
			return harukiAPIHelper.ErrorForbidden(c, "account is banned")
		case errors.Is(err, postgresql.ErrGameAccountDataGrantGranteeNotFound):
			return harukiAPIHelper.ErrorNotFound(c, "grantee user not found")
		case errors.Is(err, postgresql.ErrGameAccountDataGrantGranteeBanned):
			return harukiAPIHelper.ErrorForbidden(c, "grantee user is banned")
		case errors.Is(err, postgresql.ErrGameAccountDataGrantBindingNotFound):
			return harukiAPIHelper.ErrorNotFound(c, "binding not found")
		case errors.Is(err, postgresql.ErrGameAccountDataGrantBindingUnverified):
			return harukiAPIHelper.ErrorBadRequest(c, "binding is not verified yet")
		case err != nil:
			harukiLogger.Errorf("Failed to upsert game account data grant: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to save game account data grant")
		}
		resp := gameAccountDataGrantMutationResponse{
			GeneratedAt: gameAccountGrantNowUTC(),
			Grant:       buildGameAccountDataGrantItemFromRow(row),
		}
		userCoreModule.WriteUserAuditLog(c, apiHelper, "user.game_account_data_grant.upsert", harukiAPIHelper.SystemLogResultSuccess, ownerUserID, map[string]any{
			"server":              string(server),
			"gameUserID":          gameUserID,
			"dataType":            dataType,
			"granteeUserID":       granteeUserID,
			"expiresAt":           expiresAt.Format(time.RFC3339),
			"permissions":         postgresql.GrantPermissions(row.CanRead, row.CanWrite),
			"previousPermissions": previousPermissions,
		})
		return harukiAPIHelper.Responses.SuccessResponse(c, "game account data grant saved", &resp)
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
		return harukiAPIHelper.Responses.SuccessResponse[string](c, "game account data grant deleted", nil)
	}
}

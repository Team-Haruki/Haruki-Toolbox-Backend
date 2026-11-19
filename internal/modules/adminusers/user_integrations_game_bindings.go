package adminusers

import (
	"strconv"
	"strings"

	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountbinding"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v3"
)

func handleListUserGameAccountBindings(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminAuditActionUserGameBindingsList
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to resolve target user")
		}

		rows, err := apiHelper.DBManager.DB.GameAccountBinding.Query().
			Where(gameaccountbinding.HasUserWith(userSchema.IDEQ(targetUser.ID))).
			All(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryBindingsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query game account bindings")
		}

		resp := adminUserGameBindingsResponse{
			GeneratedAt: adminNowUTC(),
			UserID:      targetUser.ID,
			Total:       len(rows),
			Items:       buildAdminGameBindingItems(rows),
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"total": resp.Total,
		})
		return harukiAPIHelper.Responses.SuccessResponse(c, "success", &resp)
	}
}

func handleUpsertUserGameAccountBinding(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminAuditActionUserGameBindingUpsert
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to resolve target user")
		}

		serverRaw := strings.TrimSpace(c.Params("server"))
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverRaw)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidServer, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid server")
		}
		gameUserID := strings.TrimSpace(c.Params("game_user_id"))
		if gameUserID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingGameUserId, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "game_user_id is required")
		}
		numericID, err := strconv.ParseInt(gameUserID, 10, 64)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidGameUserId, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "game_user_id must be numeric")
		}
		gameUserID = strconv.FormatInt(numericID, 10)

		payload, err := parseAdminGameBindingPayload(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}
		release, err := apiHelper.DBManager.DB.LockGameAccountAndUserMutations(c.Context(), string(server), gameUserID, targetUser.ID)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to reserve binding")
		}
		defer release()
		targetUser, err = resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to resolve target user")
		}

		existing, err := apiHelper.DBManager.DB.GameAccountBinding.Query().
			Where(
				gameaccountbinding.ServerEQ(string(server)),
				gameaccountbinding.GameUserIDEQ(gameUserID),
			).
			WithUser().
			Only(c.Context())
		if err != nil && !postgresql.IsNotFound(err) {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryBindingFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query existing binding")
		}

		created := false
		if existing != nil {
			if existing.Edges.User == nil || existing.Edges.User.ID != targetUser.ID {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonBindingOwnedByOtherUser, map[string]any{
					"server":     string(server),
					"gameUserID": gameUserID,
				}))
				return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusConflict, "binding belongs to another user", nil)
			}

			if err := apiHelper.DBManager.DB.ChangeGameAccountBinding(c.Context(), string(server), gameUserID, !existing.Verified, func(db *postgresql.Client) error {
				return db.GameAccountBinding.UpdateOneID(existing.ID).Where(gameaccountbinding.HasUserWith(userSchema.IDEQ(targetUser.ID))).
					SetVerified(true).
					SetSuite(payload.Suite).
					SetMysekai(payload.MySekai).
					Exec(c.Context())
			}); err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateBindingFailed, nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to update game account binding")
			}
			clearManagedBindingPublicCaches(c.Context(), apiHelper, string(server), gameUserID)
		} else {
			created = true
			if err := apiHelper.DBManager.DB.ChangeGameAccountBinding(c.Context(), string(server), gameUserID, true, func(db *postgresql.Client) error {
				return db.GameAccountBinding.Create().
					SetServer(string(server)).
					SetGameUserID(gameUserID).
					SetVerified(true).
					SetSuite(payload.Suite).
					SetMysekai(payload.MySekai).
					SetUserID(targetUser.ID).
					Exec(c.Context())
			}); err != nil {
				if postgresql.IsConstraintError(err) {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonBindingConflict, nil))
					return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusConflict, "binding conflict", nil)
				}
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCreateBindingFailed, nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to create game account binding")
			}
		}

		resp := adminUserGameBindingUpsertResponse{
			UserID:     targetUser.ID,
			Server:     string(server),
			GameUserID: gameUserID,
			Created:    created,
			Binding: harukiAPIHelper.GameAccountBinding{
				Server:   server,
				UserID:   gameUserID,
				Verified: true,
				Suite:    payload.Suite,
				Mysekai:  payload.MySekai,
			},
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"server":     string(server),
			"gameUserID": gameUserID,
			"created":    created,
		})
		return harukiAPIHelper.Responses.SuccessResponse(c, "game account binding upserted", &resp)
	}
}

func handleDeleteUserGameAccountBinding(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminAuditActionUserGameBindingDelete
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to resolve target user")
		}

		serverRaw := strings.TrimSpace(c.Params("server"))
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverRaw)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidServer, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid server")
		}
		gameUserID := strings.TrimSpace(c.Params("game_user_id"))
		if gameUserID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingGameUserId, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "game_user_id is required")
		}

		numericID, err := strconv.ParseInt(gameUserID, 10, 64)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "game_user_id must be numeric")
		}
		gameUserID = strconv.FormatInt(numericID, 10)
		release, err := apiHelper.DBManager.DB.LockGameAccountAndUserMutations(c.Context(), string(server), gameUserID, targetUser.ID)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to reserve binding")
		}
		defer release()
		targetUser, err = resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to resolve target user")
		}
		existing, err := apiHelper.DBManager.DB.GameAccountBinding.Query().
			Where(
				gameaccountbinding.ServerEQ(string(server)),
				gameaccountbinding.GameUserIDEQ(gameUserID),
			).
			WithUser().
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonBindingNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "binding not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryBindingFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query binding")
		}

		if existing.Edges.User == nil || existing.Edges.User.ID != targetUser.ID {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonBindingOwnedByOtherUser, map[string]any{
				"server":     string(server),
				"gameUserID": gameUserID,
			}))
			return harukiAPIHelper.Responses.UpdatedDataResponse[string](c, fiber.StatusConflict, "binding belongs to another user", nil)
		}

		if err := apiHelper.DBManager.DB.ChangeGameAccountBinding(c.Context(), string(server), gameUserID, true, func(db *postgresql.Client) error {
			return db.GameAccountBinding.DeleteOne(existing).Where(gameaccountbinding.HasUserWith(userSchema.IDEQ(targetUser.ID))).Exec(c.Context())
		}); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDeleteBindingFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to delete binding")
		}
		clearManagedBindingPublicCaches(c.Context(), apiHelper, string(server), gameUserID)

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"server":     string(server),
			"gameUserID": gameUserID,
		})
		return harukiAPIHelper.Responses.SuccessResponse[string](c, "game account binding deleted", nil)
	}
}

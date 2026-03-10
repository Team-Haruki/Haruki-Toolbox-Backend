package adminusers

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"strconv"
	"strings"

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
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleUpdateUserEmail(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminAuditActionUserEmailUpdate
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to resolve target user")
		}
		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		payload, err := parseAdminManagedEmailPayload(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}

		userConflict, err := apiHelper.DBManager.DB.User.Query().
			Where(
				userSchema.EmailEqualFold(payload.Email),
				userSchema.IDNEQ(targetUser.ID),
			).
			Exist(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryUserConflictFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to check email conflict")
		}
		if userConflict {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonEmailConflict, nil))
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "email already in use", nil)
		}

		affected, err := applyManagedTargetUserUpdateGuards(
			apiHelper.DBManager.DB.User.Update().SetEmail(payload.Email),
			actorUserID,
			actorRole,
			targetUser.ID,
		).Save(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateUserEmailFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to update user email")
		}
		if affected == 0 {
			missErr := resolveManagedTargetUserUpdateMiss(c, apiHelper, actorUserID, actorRole, targetUser.ID)
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, map[string]any{
				"guardedUpdateMiss": true,
			}))
			return adminCoreModule.RespondFiberOrInternal(c, missErr, "failed to update user email")
		}

		resp := adminUserEmailResponse{
			UserID:   targetUser.ID,
			Email:    payload.Email,
			Verified: true,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"email":    payload.Email,
			"verified": true,
		})
		return harukiAPIHelper.SuccessResponse(c, "user email updated", &resp)
	}
}

func handleUpdateUserAllowCNMysekai(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminAuditActionUserAllowCNUpdate
		targetUser, err := resolveManageableTargetUser(c, apiHelper, action)
		if err != nil {
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to resolve target user")
		}
		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		payload, err := parseAdminUpdateAllowCNMysekaiPayload(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}

		affected, err := applyManagedTargetUserUpdateGuards(
			apiHelper.DBManager.DB.User.Update().SetAllowCnMysekai(*payload.AllowCNMysekai),
			actorUserID,
			actorRole,
			targetUser.ID,
		).Save(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateAllowCnMysekaiFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to update allow_cn_mysekai")
		}
		if affected == 0 {
			missErr := resolveManagedTargetUserUpdateMiss(c, apiHelper, actorUserID, actorRole, targetUser.ID)
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, map[string]any{
				"guardedUpdateMiss": true,
			}))
			return adminCoreModule.RespondFiberOrInternal(c, missErr, "failed to update allow_cn_mysekai")
		}

		updated, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(targetUser.ID)).
			Select(userSchema.FieldID, userSchema.FieldAllowCnMysekai).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTargetUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query user")
		}

		resp := adminUserAllowCNMysekaiResponse{
			UserID:         updated.ID,
			AllowCNMysekai: updated.AllowCnMysekai,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"allowCNMysekai": updated.AllowCnMysekai,
		})
		return harukiAPIHelper.SuccessResponse(c, "allow_cn_mysekai updated", &resp)
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
		if _, err := strconv.Atoi(gameUserID); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidGameUserId, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "game_user_id must be numeric")
		}

		payload, err := parseAdminGameBindingPayload(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
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
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "binding belongs to another user", nil)
			}

			if _, err := existing.Update().
				SetVerified(true).
				SetSuite(payload.Suite).
				SetMysekai(payload.MySekai).
				Save(c.Context()); err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateBindingFailed, nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to update game account binding")
			}
		} else {
			created = true
			if _, err := apiHelper.DBManager.DB.GameAccountBinding.Create().
				SetServer(string(server)).
				SetGameUserID(gameUserID).
				SetVerified(true).
				SetSuite(payload.Suite).
				SetMysekai(payload.MySekai).
				SetUserID(targetUser.ID).
				Save(c.Context()); err != nil {
				if postgresql.IsConstraintError(err) {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonBindingConflict, nil))
					return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "binding conflict", nil)
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
		return harukiAPIHelper.SuccessResponse(c, "game account binding upserted", &resp)
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
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "binding belongs to another user", nil)
		}

		if err := apiHelper.DBManager.DB.GameAccountBinding.DeleteOne(existing).Exec(c.Context()); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDeleteBindingFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to delete binding")
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"server":     string(server),
			"gameUserID": gameUserID,
		})
		return harukiAPIHelper.SuccessResponse[string](c, "game account binding deleted", nil)
	}
}

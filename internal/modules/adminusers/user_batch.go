package adminusers

import (
	"context"
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func parseBatchUserRoleUpdatePayload(c fiber.Ctx) (*batchUserRoleUpdatePayload, error) {
	var payload batchUserRoleUpdatePayload
	if err := c.Bind().Body(&payload); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	userIDs, err := sanitizeBatchUserIDs(payload.UserIDs)
	if err != nil {
		return nil, err
	}

	roleRaw := strings.TrimSpace(payload.Role)
	if roleRaw == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "role is required")
	}
	normalizedRole := adminCoreModule.NormalizeRole(roleRaw)
	if !adminCoreModule.IsValidRole(normalizedRole) {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid role")
	}

	return &batchUserRoleUpdatePayload{
		UserIDs: userIDs,
		Role:    normalizedRole,
	}, nil
}

func parseBatchUserAllowCNMysekaiUpdatePayload(c fiber.Ctx) (*batchUserAllowCNMysekaiUpdatePayload, error) {
	var payload batchUserAllowCNMysekaiUpdatePayload
	if err := c.Bind().Body(&payload); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	userIDs, err := sanitizeBatchUserIDs(payload.UserIDs)
	if err != nil {
		return nil, err
	}

	allow := payload.AllowCNMysekai
	if allow == nil {
		allow = payload.AllowCNMysekaiSnake
	}
	if allow == nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "allowCNMysekai is required")
	}

	return &batchUserAllowCNMysekaiUpdatePayload{
		UserIDs:        userIDs,
		AllowCNMysekai: allow,
	}, nil
}

func sanitizeBatchUserIDs(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "userIds is required")
	}

	seen := make(map[string]struct{}, len(raw))
	result := make([]string, 0, len(raw))
	for _, id := range raw {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}

	if len(result) == 0 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "userIds is required")
	}
	if len(result) > maxBatchUserOperationCount {
		return nil, fiber.NewError(fiber.StatusBadRequest, "too many userIds in one batch")
	}
	return result, nil
}

func executeBatchBan(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, actorUserID, actorRole, targetUserID string, reason *string) (int, error) {
	update := applyManagedTargetUserUpdateGuards(
		apiHelper.DBManager.DB.User.Update().SetBanned(true),
		actorUserID,
		actorRole,
		targetUserID,
	)
	if reason != nil {
		update.SetBanReason(*reason)
	} else {
		update.ClearBanReason()
	}
	return update.Save(ctx)
}

func executeBatchUnban(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, actorUserID, actorRole, targetUserID string) (int, error) {
	return applyManagedTargetUserUpdateGuards(
		apiHelper.DBManager.DB.User.Update().
			SetBanned(false).
			ClearBanReason(),
		actorUserID,
		actorRole,
		targetUserID,
	).Save(ctx)
}

func executeBatchForceLogout(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, targetUserID string, kratosIdentityID *string) error {
	kratosRevokeFailed := false
	if apiHelper != nil && apiHelper.SessionHandler != nil && apiHelper.SessionHandler.UsesKratosProvider() &&
		kratosIdentityID != nil && strings.TrimSpace(*kratosIdentityID) != "" {
		if err := apiHelper.SessionHandler.RevokeKratosSessionsByIdentityID(ctx, strings.TrimSpace(*kratosIdentityID)); err != nil {
			kratosRevokeFailed = true
		}
	}
	if err := harukiAPIHelper.ClearUserSessions(apiHelper.DBManager.Redis.Redis, targetUserID); err != nil {
		return err
	}
	if kratosRevokeFailed {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to revoke kratos sessions")
	}
	return nil
}

func batchUserOperationFailureMessage(action string) string {
	switch action {
	case adminBatchActionBan:
		return "failed to ban user"
	case adminBatchActionUnban:
		return "failed to unban user"
	case adminBatchActionForceLogout:
		return "failed to force logout user"
	default:
		return "operation failed"
	}
}

func mapBatchManagedUpdateMiss(err error) (string, string) {
	fiberErr, ok := err.(*fiber.Error)
	if !ok {
		return adminBatchResultCodeOperationFailed, "operation failed"
	}

	switch fiberErr.Code {
	case fiber.StatusNotFound:
		return adminBatchResultCodeUserNotFound, "user not found"
	case fiber.StatusForbidden, fiber.StatusBadRequest:
		return adminFailureReasonPermissionDenied, "insufficient permissions"
	default:
		return adminBatchResultCodeOperationFailed, "operation failed"
	}
}

func handleBatchUserOperation(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, action string) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		var payload batchUserOperationPayload
		if err := c.Bind().Body(&payload); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserBatchPrefix+action, adminAuditTargetTypeUser, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		userIDs, err := sanitizeBatchUserIDs(payload.UserIDs)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserBatchPrefix+action, adminAuditTargetTypeUser, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidUserIds, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid userIds")
		}

		var reason *string
		if action == adminBatchActionBan {
			reason, err = sanitizeBanReason(payload.Reason)
			if err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserBatchPrefix+action, adminAuditTargetTypeUser, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidReason, nil))
				return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid reason")
			}
		}

		results := make([]batchUserOperationItemResult, 0, len(userIDs))
		successCount := 0
		for _, targetUserID := range userIDs {
			item := batchUserOperationItemResult{UserID: targetUserID}

			targetUser, err := apiHelper.DBManager.DB.User.Query().
				Where(userSchema.IDEQ(targetUserID)).
				Select(userSchema.FieldID, userSchema.FieldRole, userSchema.FieldKratosIdentityID).
				Only(c.Context())
			if err != nil {
				item.Code = adminBatchResultCodeUserNotFound
				item.Message = "user not found"
				results = append(results, item)
				continue
			}

			if err := adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
				item.Code = adminFailureReasonPermissionDenied
				item.Message = "insufficient permissions"
				results = append(results, item)
				continue
			}

			affected := 1
			switch action {
			case adminBatchActionBan:
				affected, err = executeBatchBan(c.Context(), apiHelper, actorUserID, actorRole, targetUser.ID, reason)
			case adminBatchActionUnban:
				affected, err = executeBatchUnban(c.Context(), apiHelper, actorUserID, actorRole, targetUser.ID)
			case adminBatchActionForceLogout:
				err = executeBatchForceLogout(c.Context(), apiHelper, targetUser.ID, targetUser.KratosIdentityID)
			default:
				err = fiber.NewError(fiber.StatusBadRequest, "unsupported batch action")
			}
			if err != nil {
				item.Code = adminBatchResultCodeOperationFailed
				item.Message = batchUserOperationFailureMessage(action)
				results = append(results, item)
				continue
			}
			if (action == adminBatchActionBan || action == adminBatchActionUnban) && affected == 0 {
				missErr := resolveManagedTargetUserUpdateMiss(c, apiHelper, actorUserID, actorRole, targetUser.ID)
				item.Code, item.Message = mapBatchManagedUpdateMiss(missErr)
				results = append(results, item)
				continue
			}

			item.Success = true
			successCount++
			results = append(results, item)
		}

		resp := batchUserOperationResponse{
			Action:  action,
			Total:   len(userIDs),
			Success: successCount,
			Failed:  len(userIDs) - successCount,
			Results: results,
		}
		resultState := harukiAPIHelper.SystemLogResultSuccess
		if resp.Failed > 0 {
			resultState = harukiAPIHelper.SystemLogResultFailure
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserBatchPrefix+action, adminAuditTargetTypeUser, "", resultState, map[string]any{
			"total":   resp.Total,
			"success": resp.Success,
			"failed":  resp.Failed,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleBatchBanUsers(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return handleBatchUserOperation(apiHelper, adminBatchActionBan)
}

func handleBatchUnbanUsers(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return handleBatchUserOperation(apiHelper, adminBatchActionUnban)
}

func handleBatchForceLogoutUsers(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return handleBatchUserOperation(apiHelper, adminBatchActionForceLogout)
}

func handleBatchUpdateUserRole(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		payload, err := parseBatchUserRoleUpdatePayload(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserBatchRole, adminAuditTargetTypeUser, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}

		results := make([]batchUserOperationItemResult, 0, len(payload.UserIDs))
		successCount := 0
		for _, targetUserID := range payload.UserIDs {
			item := batchUserOperationItemResult{UserID: targetUserID}

			targetUser, err := apiHelper.DBManager.DB.User.Query().
				Where(userSchema.IDEQ(targetUserID)).
				Select(userSchema.FieldID, userSchema.FieldRole, userSchema.FieldKratosIdentityID).
				Only(c.Context())
			if err != nil {
				item.Code = adminBatchResultCodeUserNotFound
				item.Message = "user not found"
				results = append(results, item)
				continue
			}

			if err := adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
				item.Code = adminFailureReasonPermissionDenied
				item.Message = "insufficient permissions"
				results = append(results, item)
				continue
			}

			affected, err := applyManagedTargetUserUpdateGuards(
				apiHelper.DBManager.DB.User.Update().SetRole(userSchema.Role(payload.Role)),
				actorUserID,
				actorRole,
				targetUser.ID,
			).Save(c.Context())
			if err != nil {
				item.Code = adminBatchResultCodeOperationFailed
				item.Message = "failed to update user role"
				results = append(results, item)
				continue
			}
			if affected == 0 {
				missErr := resolveManagedTargetUserUpdateMiss(c, apiHelper, actorUserID, actorRole, targetUser.ID)
				item.Code, item.Message = mapBatchManagedUpdateMiss(missErr)
				results = append(results, item)
				continue
			}

			item.Success = true
			successCount++
			results = append(results, item)
		}

		resp := batchUserOperationResponse{
			Action:  adminBatchActionRoleUpdate,
			Total:   len(payload.UserIDs),
			Success: successCount,
			Failed:  len(payload.UserIDs) - successCount,
			Results: results,
		}
		resultState := harukiAPIHelper.SystemLogResultSuccess
		if resp.Failed > 0 {
			resultState = harukiAPIHelper.SystemLogResultFailure
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserBatchRole, adminAuditTargetTypeUser, "", resultState, map[string]any{
			"role":    payload.Role,
			"total":   resp.Total,
			"success": resp.Success,
			"failed":  resp.Failed,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleBatchUpdateUserAllowCNMysekai(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		payload, err := parseBatchUserAllowCNMysekaiUpdatePayload(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserBatchAllowCN, adminAuditTargetTypeUser, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}

		results := make([]batchUserOperationItemResult, 0, len(payload.UserIDs))
		successCount := 0
		for _, targetUserID := range payload.UserIDs {
			item := batchUserOperationItemResult{UserID: targetUserID}

			targetUser, err := apiHelper.DBManager.DB.User.Query().
				Where(userSchema.IDEQ(targetUserID)).
				Select(userSchema.FieldID, userSchema.FieldRole, userSchema.FieldKratosIdentityID).
				Only(c.Context())
			if err != nil {
				item.Code = adminBatchResultCodeUserNotFound
				item.Message = "user not found"
				results = append(results, item)
				continue
			}

			if err := adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
				item.Code = adminFailureReasonPermissionDenied
				item.Message = "insufficient permissions"
				results = append(results, item)
				continue
			}

			affected, err := applyManagedTargetUserUpdateGuards(
				apiHelper.DBManager.DB.User.Update().SetAllowCnMysekai(*payload.AllowCNMysekai),
				actorUserID,
				actorRole,
				targetUser.ID,
			).Save(c.Context())
			if err != nil {
				item.Code = adminBatchResultCodeOperationFailed
				item.Message = "failed to update allow_cn_mysekai"
				results = append(results, item)
				continue
			}
			if affected == 0 {
				missErr := resolveManagedTargetUserUpdateMiss(c, apiHelper, actorUserID, actorRole, targetUser.ID)
				item.Code, item.Message = mapBatchManagedUpdateMiss(missErr)
				results = append(results, item)
				continue
			}

			item.Success = true
			successCount++
			results = append(results, item)
		}

		resp := batchUserOperationResponse{
			Action:  adminBatchActionAllowCN,
			Total:   len(payload.UserIDs),
			Success: successCount,
			Failed:  len(payload.UserIDs) - successCount,
			Results: results,
		}
		resultState := harukiAPIHelper.SystemLogResultSuccess
		if resp.Failed > 0 {
			resultState = harukiAPIHelper.SystemLogResultFailure
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserBatchAllowCN, adminAuditTargetTypeUser, "", resultState, map[string]any{
			"allowCNMysekai": *payload.AllowCNMysekai,
			"total":          resp.Total,
			"success":        resp.Success,
			"failed":         resp.Failed,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

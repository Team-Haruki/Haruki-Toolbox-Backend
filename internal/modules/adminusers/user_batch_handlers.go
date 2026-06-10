package adminusers

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"

	"github.com/gofiber/fiber/v3"
)

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

		// Batch fetch all users at once to avoid N+1 queries
		userMap, err := batchFetchUsers(c.Context(), apiHelper, userIDs)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserBatchPrefix+action, adminAuditTargetTypeUser, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDatabaseError, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to fetch users")
		}

		for _, targetUserID := range userIDs {
			item := batchUserOperationItemResult{UserID: targetUserID}

			targetUser, found := userMap[targetUserID]
			if !found {
				item.Code = adminBatchResultCodeUserNotFound
				item.Message = "user not found"
				results = append(results, item)
				continue
			}

			if err := adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, targetUser.Role); err != nil {
				item.Code = adminFailureReasonPermissionDenied
				item.Message = "insufficient permissions"
				results = append(results, item)
				continue
			}

			affected := 1
			switch action {
			case adminBatchActionBan:
				affected, err = executeBatchBan(c.Context(), apiHelper, actorUserID, actorRole, targetUser.ID, reason)
				if err == nil {
					sessionClearFailed, oauthRevokeFailed := cleanupManagedUserAccessAfterBan(c.Context(), apiHelper, targetUser.ID, targetUser.KratosIdentityID)
					if sessionClearFailed || oauthRevokeFailed {
						item.Message, _ = resolveManagedUserBanFinalizeOutcome(sessionClearFailed, oauthRevokeFailed)
					}
				}
			case adminBatchActionUnban:
				affected, err = executeBatchUnban(c.Context(), apiHelper, actorUserID, actorRole, targetUser.ID)
			case adminBatchActionForceLogout:
				err = executeBatchForceLogout(c.Context(), apiHelper, targetUser.ID, targetUser.KratosIdentityID)
			default:
				err = fiber.NewError(fiber.StatusBadRequest, "unsupported batch action")
			}
			if err != nil {
				if item.Code == "" {
					item.Code = adminBatchResultCodeOperationFailed
				}
				if item.Message == "" {
					item.Message = batchUserOperationFailureMessage(action)
				}
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

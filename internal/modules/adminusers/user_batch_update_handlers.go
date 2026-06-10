package adminusers

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"
	userSchema "haruki-suite/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v3"
)

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

		// Batch fetch all users at once to avoid N+1 queries
		userMap, err := batchFetchUsers(c.Context(), apiHelper, payload.UserIDs)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserBatchRole, adminAuditTargetTypeUser, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDatabaseError, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to fetch users")
		}

		for _, targetUserID := range payload.UserIDs {
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

		// Batch fetch all users at once to avoid N+1 queries
		userMap, err := batchFetchUsers(c.Context(), apiHelper, payload.UserIDs)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserBatchAllowCN, adminAuditTargetTypeUser, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDatabaseError, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to fetch users")
		}

		for _, targetUserID := range payload.UserIDs {
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

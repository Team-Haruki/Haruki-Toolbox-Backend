package adminusers

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	platformPagination "haruki-suite/internal/platform/pagination"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func handleListUsers(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		filters, err := parseAdminUserQueryFilters(c)
		if err != nil {
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid query filters")
		}

		dbCtx := c.Context()
		baseQuery := applyAdminUserQueryFilters(apiHelper.DBManager.DB.User.Query(), filters)

		total, err := baseQuery.Clone().Count(dbCtx)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to count users")
		}

		offset := (filters.Page - 1) * filters.PageSize
		rows, err := applyAdminUsersSort(baseQuery.Clone(), filters.Sort).
			Limit(filters.PageSize).
			Offset(offset).
			Select(
				userSchema.FieldID,
				userSchema.FieldName,
				userSchema.FieldEmail,
				userSchema.FieldRole,
				userSchema.FieldBanned,
				userSchema.FieldAllowCnMysekai,
				userSchema.FieldBanReason,
				userSchema.FieldCreatedAt,
			).
			All(dbCtx)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query users")
		}

		totalPages := platformPagination.CalculateTotalPages(total, filters.PageSize)

		resp := adminUserListResponse{
			GeneratedAt: adminNowUTC(),
			Page:        filters.Page,
			PageSize:    filters.PageSize,
			Total:       total,
			TotalPages:  totalPages,
			HasMore:     platformPagination.HasMoreByOffset(filters.Page, filters.PageSize, total),
			Sort:        filters.Sort,
			Filters: adminUserAppliedFilters{
				Query:          filters.Query,
				Role:           filters.Role,
				Banned:         filters.Banned,
				AllowCNMysekai: filters.AllowCNMysekai,
				CreatedFrom:    filters.CreatedFrom,
				CreatedTo:      filters.CreatedTo,
			},
			Items: buildAdminUserListItems(rows),
		}

		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleBanUser(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetUserID := strings.TrimSpace(c.Params("target_user_id"))
		if targetUserID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserBan, adminAuditTargetTypeUser, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingTargetUserID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "target_user_id is required")
		}

		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserBan, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		var payload updateUserBanPayload
		if len(c.Body()) > 0 {
			if err := c.Bind().Body(&payload); err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserBan, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
				return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
			}
		}

		reason, err := sanitizeBanReason(payload.Reason)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserBan, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidBanReason, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid ban reason")
		}

		targetUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(targetUserID)).
			Select(
				userSchema.FieldID,
				userSchema.FieldRole,
				userSchema.FieldBanned,
				userSchema.FieldBanReason,
				userSchema.FieldKratosIdentityID,
			).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserBan, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserBan, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTargetUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}

		if err := adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserBan, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, map[string]any{
				"actorRole":  actorRole,
				"targetRole": adminCoreModule.NormalizeRole(string(targetUser.Role)),
			}))
			return adminCoreModule.RespondFiberOrForbidden(c, err, "insufficient permissions")
		}

		update := applyManagedTargetUserUpdateGuards(
			apiHelper.DBManager.DB.User.Update().SetBanned(true),
			actorUserID,
			actorRole,
			targetUser.ID,
		)
		if reason != nil {
			update.SetBanReason(*reason)
		} else {
			update.ClearBanReason()
		}

		affected, err := update.Save(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserBan, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonBanUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to ban user")
		}
		if affected == 0 {
			missErr := resolveManagedTargetUserUpdateMiss(c, apiHelper, actorUserID, actorRole, targetUser.ID)
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserBan, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, map[string]any{
				"guardedUpdateMiss": true,
			}))
			return adminCoreModule.RespondFiberOrInternal(c, missErr, "failed to ban user")
		}

		updatedUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(targetUser.ID)).
			Select(userSchema.FieldID, userSchema.FieldRole, userSchema.FieldBanned, userSchema.FieldBanReason).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserBan, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserBan, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTargetUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}

		resp := userBanStatusResponse{
			UserID:    updatedUser.ID,
			Role:      adminCoreModule.NormalizeRole(string(updatedUser.Role)),
			Banned:    updatedUser.Banned,
			BanReason: updatedUser.BanReason,
		}
		sessionClearFailed, oauthRevokeFailed := cleanupManagedUserAccessAfterBan(c.Context(), apiHelper, targetUser.ID, targetUser.KratosIdentityID)
		clearedSessions := !sessionClearFailed
		revokedOAuthTokens := !oauthRevokeFailed
		resp.ClearedSessions = &clearedSessions
		resp.RevokedOAuthTokens = &revokedOAuthTokens

		message, success := resolveManagedUserBanFinalizeOutcome(sessionClearFailed, oauthRevokeFailed)
		metadata := map[string]any{
			"hasReason":          reason != nil,
			"clearedSessions":    clearedSessions,
			"revokedOAuthTokens": revokedOAuthTokens,
			"sessionClearFailed": sessionClearFailed,
			"oauthRevokeFailed":  oauthRevokeFailed,
		}
		resultState := harukiAPIHelper.SystemLogResultSuccess
		if !success {
			resultState = harukiAPIHelper.SystemLogResultFailure
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserBan, adminAuditTargetTypeUser, updatedUser.ID, resultState, metadata)
		return harukiAPIHelper.SuccessResponse(c, message, &resp)
	}
}

func handleUnbanUser(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetUserID := strings.TrimSpace(c.Params("target_user_id"))
		if targetUserID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserUnban, adminAuditTargetTypeUser, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingTargetUserID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "target_user_id is required")
		}

		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserUnban, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		targetUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(targetUserID)).
			Select(userSchema.FieldID, userSchema.FieldRole).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserUnban, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserUnban, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTargetUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}

		if err := adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserUnban, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, map[string]any{
				"actorRole":  actorRole,
				"targetRole": adminCoreModule.NormalizeRole(string(targetUser.Role)),
			}))
			return adminCoreModule.RespondFiberOrForbidden(c, err, "insufficient permissions")
		}

		affected, err := applyManagedTargetUserUpdateGuards(
			apiHelper.DBManager.DB.User.Update().
				SetBanned(false).
				ClearBanReason(),
			actorUserID,
			actorRole,
			targetUser.ID,
		).Save(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserUnban, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUnbanUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to unban user")
		}
		if affected == 0 {
			missErr := resolveManagedTargetUserUpdateMiss(c, apiHelper, actorUserID, actorRole, targetUser.ID)
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserUnban, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, map[string]any{
				"guardedUpdateMiss": true,
			}))
			return adminCoreModule.RespondFiberOrInternal(c, missErr, "failed to unban user")
		}

		updatedUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(targetUser.ID)).
			Select(userSchema.FieldID, userSchema.FieldRole, userSchema.FieldBanned).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserUnban, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserUnban, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTargetUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}

		resp := userBanStatusResponse{
			UserID: updatedUser.ID,
			Role:   adminCoreModule.NormalizeRole(string(updatedUser.Role)),
			Banned: updatedUser.Banned,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserUnban, adminAuditTargetTypeUser, updatedUser.ID, harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse(c, "user unbanned", &resp)
	}
}

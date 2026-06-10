package adminusers

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	"strings"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

func parseAdminTicketNotificationPreferencePayload(c fiber.Ctx) (*adminTicketNotificationPreferencePayload, error) {
	var payload adminTicketNotificationPreferencePayload
	if err := c.Bind().Body(&payload); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	enabled := payload.TicketEmailNotificationsEnabled
	if enabled == nil {
		enabled = payload.TicketEmailNotificationsEnabledSnake
	}
	if enabled == nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "ticketEmailNotificationsEnabled is required")
	}

	return &adminTicketNotificationPreferencePayload{
		TicketEmailNotificationsEnabled: enabled,
	}, nil
}

func isAdminTicketNotificationRecipientRole(role string) bool {
	normalizedRole := adminCoreModule.NormalizeRole(role)
	return normalizedRole == adminCoreModule.RoleAdmin || normalizedRole == adminCoreModule.RoleSuperAdmin
}

func queryAdminTicketNotificationTargetUser(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, action string) (*postgresql.User, error) {
	targetUserID := strings.TrimSpace(c.Params("target_user_id"))
	if targetUserID == "" {
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingTargetUserID, nil))
		return nil, fiber.NewError(fiber.StatusBadRequest, "target_user_id is required")
	}

	_, _, err := adminCoreModule.CurrentAdminActor(c)
	if err != nil {
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
		return nil, err
	}

	targetUser, err := apiHelper.DBManager.DB.User.Query().
		Where(user.IDEQ(targetUserID)).
		Select(
			user.FieldID,
			user.FieldName,
			user.FieldEmail,
			user.FieldRole,
			user.FieldBanned,
			user.FieldTicketEmailNotificationsEnabled,
		).
		Only(c.Context())
	if err != nil {
		if postgresql.IsNotFound(err) {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, nil))
			return nil, fiber.NewError(fiber.StatusNotFound, "user not found")
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTargetUserFailed, nil))
		return nil, fiber.NewError(fiber.StatusInternalServerError, "failed to query target user")
	}

	if !isAdminTicketNotificationRecipientRole(string(targetUser.Role)) {
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidTicketNotificationTarget, map[string]any{
			"targetRole": adminCoreModule.NormalizeRole(string(targetUser.Role)),
		}))
		return nil, fiber.NewError(fiber.StatusBadRequest, "ticket notifications can only be managed for admin users")
	}
	return targetUser, nil
}

func buildAdminTicketNotificationRecipientItems(rows []*postgresql.User) []adminTicketNotificationRecipientItem {
	items := make([]adminTicketNotificationRecipientItem, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		items = append(items, adminTicketNotificationRecipientItem{
			UserID:                          row.ID,
			Name:                            row.Name,
			Email:                           row.Email,
			Role:                            adminCoreModule.NormalizeRole(string(row.Role)),
			Banned:                          row.Banned,
			TicketEmailNotificationsEnabled: row.TicketEmailNotificationsEnabled,
		})
	}
	return items
}

func handleListAdminTicketNotificationRecipients(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminAuditActionUserTicketNotifList
		if _, _, err := adminCoreModule.CurrentAdminActor(c); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		rows, err := apiHelper.DBManager.DB.User.Query().
			Where(user.RoleIn(user.RoleAdmin, user.RoleSuperAdmin)).
			Order(user.ByRole(sql.OrderDesc()), user.ByName(sql.OrderAsc()), user.ByID(sql.OrderAsc())).
			Select(
				user.FieldID,
				user.FieldName,
				user.FieldEmail,
				user.FieldRole,
				user.FieldBanned,
				user.FieldTicketEmailNotificationsEnabled,
			).
			All(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTicketNotificationRecipientsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query ticket notification recipients")
		}

		resp := adminTicketNotificationRecipientsResponse{
			GeneratedAt: adminNowUTC(),
			Total:       len(rows),
			Items:       buildAdminTicketNotificationRecipientItems(rows),
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, "", harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"total": resp.Total,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleUpdateUserTicketNotificationPreference(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = adminAuditActionUserTicketNotifSet
		targetUser, err := queryAdminTicketNotificationTargetUser(c, apiHelper, action)
		if err != nil {
			return adminCoreModule.RespondFiberOrInternal(c, err, "failed to resolve target user")
		}

		payload, err := parseAdminTicketNotificationPreferencePayload(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}

		affected, err := apiHelper.DBManager.DB.User.Update().
			Where(
				user.IDEQ(targetUser.ID),
				user.RoleIn(user.RoleAdmin, user.RoleSuperAdmin),
			).
			SetTicketEmailNotificationsEnabled(*payload.TicketEmailNotificationsEnabled).
			Save(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateTicketNotificationFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to update ticket notification preference")
		}
		if affected == 0 {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidTicketNotificationTarget, map[string]any{
				"guardedUpdateMiss": true,
			}))
			return harukiAPIHelper.ErrorNotFound(c, "user not found")
		}

		updated, err := apiHelper.DBManager.DB.User.Query().
			Where(user.IDEQ(targetUser.ID)).
			Select(
				user.FieldID,
				user.FieldName,
				user.FieldEmail,
				user.FieldRole,
				user.FieldBanned,
				user.FieldTicketEmailNotificationsEnabled,
			).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTargetUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}

		resp := adminTicketNotificationRecipientItem{
			UserID:                          updated.ID,
			Name:                            updated.Name,
			Email:                           updated.Email,
			Role:                            adminCoreModule.NormalizeRole(string(updated.Role)),
			Banned:                          updated.Banned,
			TicketEmailNotificationsEnabled: updated.TicketEmailNotificationsEnabled,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, action, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"ticketEmailNotificationsEnabled": resp.TicketEmailNotificationsEnabled,
		})
		return harukiAPIHelper.SuccessResponse(c, "ticket notification preference updated", &resp)
	}
}

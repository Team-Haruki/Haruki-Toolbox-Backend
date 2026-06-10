package admin

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v3"
)

type adminTicketNotificationPreferencePayload struct {
	TicketEmailNotificationsEnabled *bool `json:"ticketEmailNotificationsEnabled"`
}

type adminTicketNotificationPreferenceResponse struct {
	TicketEmailNotificationsEnabled bool `json:"ticketEmailNotificationsEnabled"`
}

func buildAdminTicketNotificationPreferenceResponse(dbUser *postgresql.User) adminTicketNotificationPreferenceResponse {
	resp := adminTicketNotificationPreferenceResponse{}
	if dbUser != nil {
		resp.TicketEmailNotificationsEnabled = dbUser.TicketEmailNotificationsEnabled
	}
	return resp
}

func handleGetAdminTicketNotificationPreference(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, _, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return respondFiberOrUnauthorized(c, err, "missing user session")
		}

		dbUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(userID)).
			Select(userSchema.FieldID, userSchema.FieldTicketEmailNotificationsEnabled).
			Only(c.Context())
		if err != nil {
			reason := adminFailureReasonQueryUserFailed
			if postgresql.IsNotFound(err) {
				reason = adminFailureReasonInvalidUserSession
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeTicketNotificationsGet, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(reason, nil))
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorUnauthorized(c, "invalid user session")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to query ticket notification preference")
		}

		resp := buildAdminTicketNotificationPreferenceResponse(dbUser)
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeTicketNotificationsGet, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"ticketEmailNotificationsEnabled": resp.TicketEmailNotificationsEnabled,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleUpdateAdminTicketNotificationPreference(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, _, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return respondFiberOrUnauthorized(c, err, "missing user session")
		}

		var payload adminTicketNotificationPreferencePayload
		if err := c.Bind().Body(&payload); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeTicketNotificationsSet, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}
		if payload.TicketEmailNotificationsEnabled == nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeTicketNotificationsSet, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, map[string]any{
				"field": "ticketEmailNotificationsEnabled",
			}))
			return harukiAPIHelper.ErrorBadRequest(c, "ticketEmailNotificationsEnabled is required")
		}

		updated, err := apiHelper.DBManager.DB.User.Update().
			Where(userSchema.IDEQ(userID)).
			SetTicketEmailNotificationsEnabled(*payload.TicketEmailNotificationsEnabled).
			Save(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeTicketNotificationsSet, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to update ticket notification preference")
		}
		if updated == 0 {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeTicketNotificationsSet, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidUserSession, nil))
			return harukiAPIHelper.ErrorUnauthorized(c, "invalid user session")
		}

		dbUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(userID)).
			Select(userSchema.FieldID, userSchema.FieldTicketEmailNotificationsEnabled).
			Only(c.Context())
		if err != nil {
			reason := adminFailureReasonQueryUserFailed
			if postgresql.IsNotFound(err) {
				reason = adminFailureReasonInvalidUserSession
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeTicketNotificationsSet, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(reason, nil))
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorUnauthorized(c, "invalid user session")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to query ticket notification preference")
		}

		resp := buildAdminTicketNotificationPreferenceResponse(dbUser)
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeTicketNotificationsSet, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"ticketEmailNotificationsEnabled": resp.TicketEmailNotificationsEnabled,
		})
		return harukiAPIHelper.SuccessResponse(c, "ticket notification preference updated", &resp)
	}
}

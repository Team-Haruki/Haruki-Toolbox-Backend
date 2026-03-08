package admin

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"strings"

	"github.com/gofiber/fiber/v3"
)

type updateUserRolePayload struct {
	Role string `json:"role"`
}

type userRoleResponse struct {
	UserID string `json:"userId"`
	Role   string `json:"role"`
	Banned bool   `json:"banned"`
}

func handleGetUserRole(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetUserID := c.Params("target_user_id")
		if targetUserID == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "target_user_id is required")
		}

		dbUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(targetUserID)).
			Select(userSchema.FieldID, userSchema.FieldRole, userSchema.FieldBanned).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to query user role")
		}

		resp := userRoleResponse{
			UserID: dbUser.ID,
			Role:   normalizeRole(string(dbUser.Role)),
			Banned: dbUser.Banned,
		}
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleUpdateUserRole(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetUserID := strings.TrimSpace(c.Params("target_user_id"))
		if targetUserID == "" {
			writeAdminAuditLog(c, apiHelper, "admin.user.role.update", "user", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_target_user_id", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "target_user_id is required")
		}

		var payload updateUserRolePayload
		if err := c.Bind().Body(&payload); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.role.update", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		normalizedRole := normalizeRole(payload.Role)
		if !isValidRole(normalizedRole) {
			writeAdminAuditLog(c, apiHelper, "admin.user.role.update", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_role", map[string]any{
				"requestedRole": strings.TrimSpace(payload.Role),
			}))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid role")
		}

		updatedUser, err := apiHelper.DBManager.DB.User.UpdateOneID(targetUserID).
			SetRole(userSchema.Role(normalizedRole)).
			Save(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, "admin.user.role.update", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("target_user_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			writeAdminAuditLog(c, apiHelper, "admin.user.role.update", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("update_role_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to update user role")
		}

		resp := userRoleResponse{
			UserID: updatedUser.ID,
			Role:   normalizeRole(string(updatedUser.Role)),
			Banned: updatedUser.Banned,
		}
		writeAdminAuditLog(c, apiHelper, "admin.user.role.update", "user", updatedUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"newRole": resp.Role,
		})
		return harukiAPIHelper.SuccessResponse(c, "role updated", &resp)
	}
}

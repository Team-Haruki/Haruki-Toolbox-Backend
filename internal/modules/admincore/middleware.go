package admincore

import (
	harukiAPIHelper "haruki-suite/utils/api"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"strings"

	"github.com/gofiber/fiber/v3"
)

const (
	RoleUser       = "user"
	RoleAdmin      = "admin"
	RoleSuperAdmin = "super_admin"
)

const (
	adminAuditTargetTypeRoute = "route"
	adminAuditActionAccess    = "admin.access"
)

const (
	adminFailureReasonRoleMiddlewareMisconfigured = "role_middleware_misconfigured"
	adminFailureReasonMissingUserSession          = "missing_user_session"
	adminFailureReasonInvalidUserSession          = "invalid_user_session"
	adminFailureReasonBannedUserDenied            = "banned_user_denied"
	adminFailureReasonInsufficientPermissions     = "insufficient_permissions"
)

type userRoleLookup func(c fiber.Ctx, userID string) (role string, banned bool, err error)

type UserRoleLookup = userRoleLookup

func normalizeRole(role string) string {
	normalized := strings.ToLower(strings.TrimSpace(role))
	if normalized == "" {
		return RoleUser
	}
	return normalized
}

func isValidRole(role string) bool {
	switch normalizeRole(role) {
	case RoleUser, RoleAdmin, RoleSuperAdmin:
		return true
	default:
		return false
	}
}

func roleLookupFromDB(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) userRoleLookup {
	return func(c fiber.Ctx, userID string) (role string, banned bool, err error) {
		dbUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(userID)).
			Select(userSchema.FieldRole, userSchema.FieldBanned).
			Only(c.Context())
		if err != nil {
			return "", false, err
		}
		return string(dbUser.Role), dbUser.Banned, nil
	}
}

func adminFailureMetadata(reason string, extra map[string]any) map[string]any {
	out := make(map[string]any, 1+len(extra))
	out["reason"] = reason
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func writeAdminAuditLog(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, action string, targetType string, targetID string, result string, metadata map[string]any) {
	var targetTypePtr *string
	if normalizedTargetType := strings.TrimSpace(targetType); normalizedTargetType != "" {
		targetTypeCopy := normalizedTargetType
		targetTypePtr = &targetTypeCopy
	}

	var targetIDPtr *string
	if normalizedTargetID := strings.TrimSpace(targetID); normalizedTargetID != "" {
		targetIDCopy := normalizedTargetID
		targetIDPtr = &targetIDCopy
	}

	entry := harukiAPIHelper.BuildSystemLogEntryFromFiber(c, action, result, targetTypePtr, targetIDPtr, metadata)
	_ = harukiAPIHelper.WriteSystemLog(c.Context(), apiHelper, entry)
}

func logAdminAccessDenied(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, reason string, requiredRoles []string, resolvedRole string) {
	if apiHelper == nil {
		return
	}

	metadata := adminFailureMetadata(reason, nil)
	if len(requiredRoles) > 0 {
		metadata["requiredRoles"] = append([]string(nil), requiredRoles...)
	}
	if strings.TrimSpace(resolvedRole) != "" {
		metadata["resolvedRole"] = normalizeRole(resolvedRole)
	}
	writeAdminAuditLog(c, apiHelper, adminAuditActionAccess, adminAuditTargetTypeRoute, c.Path(), harukiAPIHelper.SystemLogResultFailure, metadata)
}

func requireAnyRole(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, lookup userRoleLookup, allowedRoles ...string) fiber.Handler {
	allowed := make(map[string]struct{}, len(allowedRoles))
	validAllowedRoles := make([]string, 0, len(allowedRoles))
	for _, role := range allowedRoles {
		normalized := normalizeRole(role)
		if isValidRole(normalized) {
			allowed[normalized] = struct{}{}
			validAllowedRoles = append(validAllowedRoles, normalized)
		}
	}

	return func(c fiber.Ctx) error {
		if len(allowed) == 0 {
			logAdminAccessDenied(c, apiHelper, adminFailureReasonRoleMiddlewareMisconfigured, nil, "")
			return harukiAPIHelper.ErrorInternal(c, "role middleware misconfigured")
		}

		userID, ok := c.Locals("userID").(string)
		if !ok || userID == "" {
			logAdminAccessDenied(c, apiHelper, adminFailureReasonMissingUserSession, validAllowedRoles, "")
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		role, banned, err := lookup(c, userID)
		if err != nil {
			logAdminAccessDenied(c, apiHelper, adminFailureReasonInvalidUserSession, validAllowedRoles, "")
			return harukiAPIHelper.ErrorUnauthorized(c, "invalid user session")
		}
		if banned {
			logAdminAccessDenied(c, apiHelper, adminFailureReasonBannedUserDenied, validAllowedRoles, role)
			return harukiAPIHelper.ErrorForbidden(c, "banned users cannot access admin routes")
		}

		normalizedRole := normalizeRole(role)
		if _, ok := allowed[normalizedRole]; !ok {
			logAdminAccessDenied(c, apiHelper, adminFailureReasonInsufficientPermissions, validAllowedRoles, normalizedRole)
			return harukiAPIHelper.ErrorForbidden(c, "insufficient permissions")
		}

		c.Locals("userRole", normalizedRole)
		return c.Next()
	}
}

func RequireAdmin(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return requireAnyRole(apiHelper, roleLookupFromDB(apiHelper), RoleAdmin, RoleSuperAdmin)
}

func RequireSuperAdmin(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return requireAnyRole(apiHelper, roleLookupFromDB(apiHelper), RoleSuperAdmin)
}

func RequireAnyRoleWithLookup(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, lookup UserRoleLookup, allowedRoles ...string) fiber.Handler {
	if lookup == nil {
		return requireAnyRole(apiHelper, func(c fiber.Ctx, userID string) (string, bool, error) {
			return "", false, fiber.ErrUnauthorized
		}, allowedRoles...)
	}
	return requireAnyRole(apiHelper, lookup, allowedRoles...)
}

package admin

import (
	harukiAPIHelper "haruki-suite/utils/api"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"strings"

	"github.com/gofiber/fiber/v3"
)

const (
	roleUser       = "user"
	roleAdmin      = "admin"
	roleSuperAdmin = "super_admin"
)

type userRoleLookup func(c fiber.Ctx, userID string) (role string, banned bool, err error)

func normalizeRole(role string) string {
	normalized := strings.ToLower(strings.TrimSpace(role))
	if normalized == "" {
		return roleUser
	}
	return normalized
}

func isValidRole(role string) bool {
	switch normalizeRole(role) {
	case roleUser, roleAdmin, roleSuperAdmin:
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
	writeAdminAuditLog(c, apiHelper, "admin.access", "route", c.Path(), harukiAPIHelper.SystemLogResultFailure, metadata)
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
			logAdminAccessDenied(c, apiHelper, "role_middleware_misconfigured", nil, "")
			return harukiAPIHelper.ErrorInternal(c, "role middleware misconfigured")
		}

		userID, ok := c.Locals("userID").(string)
		if !ok || userID == "" {
			logAdminAccessDenied(c, apiHelper, "missing_user_session", validAllowedRoles, "")
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		role, banned, err := lookup(c, userID)
		if err != nil {
			logAdminAccessDenied(c, apiHelper, "invalid_user_session", validAllowedRoles, "")
			return harukiAPIHelper.ErrorUnauthorized(c, "invalid user session")
		}
		if banned {
			logAdminAccessDenied(c, apiHelper, "banned_user_denied", validAllowedRoles, role)
			return harukiAPIHelper.ErrorForbidden(c, "banned users cannot access admin routes")
		}

		normalizedRole := normalizeRole(role)
		if _, ok := allowed[normalizedRole]; !ok {
			logAdminAccessDenied(c, apiHelper, "insufficient_permissions", validAllowedRoles, normalizedRole)
			return harukiAPIHelper.ErrorForbidden(c, "insufficient permissions")
		}

		c.Locals("userRole", normalizedRole)
		return c.Next()
	}
}

func RequireAdmin(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return requireAnyRole(apiHelper, roleLookupFromDB(apiHelper), roleAdmin, roleSuperAdmin)
}

func RequireSuperAdmin(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return requireAnyRole(apiHelper, roleLookupFromDB(apiHelper), roleSuperAdmin)
}

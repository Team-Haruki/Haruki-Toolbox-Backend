package admin

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"

	"github.com/gofiber/fiber/v3"
)

func RequireRecentAdminReauth(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, _, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionAccess, adminAuditTargetTypeRoute, c.Path(), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
			return respondFiberOrUnauthorized(c, err, "missing user session")
		}

		if err := ensureCurrentAdminSessionReauthenticated(c, apiHelper, userID); err != nil {
			reason := adminFailureReasonReauthRequired
			status := fiber.StatusForbidden
			if fiberErr, ok := err.(*fiber.Error); ok {
				status = fiberErr.Code
				switch fiberErr.Code {
				case fiber.StatusUnauthorized:
					reason = adminFailureReasonInvalidUserSession
				case fiber.StatusInternalServerError:
					reason = adminFailureReasonSessionStoreUnavailable
				}
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionAccess, adminAuditTargetTypeRoute, c.Path(), harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(reason, map[string]any{
				"status": status,
			}))
			switch status {
			case fiber.StatusUnauthorized:
				return respondFiberOrUnauthorized(c, err, "invalid user session")
			case fiber.StatusInternalServerError:
				return respondFiberOrInternal(c, err, "failed to verify reauthentication")
			default:
				return respondFiberOrForbidden(c, err, "reauthentication required")
			}
		}

		return c.Next()
	}
}

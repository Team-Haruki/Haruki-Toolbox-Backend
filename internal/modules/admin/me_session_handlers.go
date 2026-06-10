package admin

import (
	"strings"

	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"

	"github.com/gofiber/fiber/v3"
)

func handleDeleteAdminSession(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, _, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return respondFiberOrUnauthorized(c, err, "missing user session")
		}

		sessionTokenID := strings.TrimSpace(c.Params("session_token_id"))
		if sessionTokenID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeSessionsDelete, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingSessionTokenId, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "session_token_id is required")
		}

		if apiHelper == nil || apiHelper.SessionHandler == nil || !apiHelper.SessionHandler.UsesKratosProvider() {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeSessionsDelete, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidUserSession, map[string]any{
				"provider": "kratos",
			}))
			return harukiAPIHelper.ErrorUnauthorized(c, "invalid user session")
		}

		ownsSession, ownErr := currentAdminOwnsKratosSession(c, apiHelper, userID, sessionTokenID)
		if ownErr != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeSessionsDelete, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDeleteSessionFailed, map[string]any{
				"provider": "kratos",
				"stage":    "ownership_check",
			}))
			if isAdminSessionIdentityNotFound(ownErr) {
				return harukiAPIHelper.ErrorUnauthorized(c, "invalid user session")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to delete session")
		}
		if !ownsSession {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeSessionsDelete, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonNothingToRevoke, map[string]any{
				"provider":       "kratos",
				"sessionTokenID": sessionTokenID,
			}))
			return harukiAPIHelper.ErrorNotFound(c, "session not found")
		}
		if err := apiHelper.SessionHandler.RevokeKratosSessionByID(harukiAPIHelper.WithHTTPRequestMetadata(c.Context(), c.Get("User-Agent"), c.IP()), sessionTokenID); err != nil {
			if statusCode, message, known := mapKratosSessionDeleteError(err); known {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeSessionsDelete, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDeleteSessionFailed, map[string]any{
					"provider":   "kratos",
					"statusCode": statusCode,
					"message":    message,
				}))
				switch statusCode {
				case fiber.StatusBadRequest:
					return harukiAPIHelper.ErrorBadRequest(c, message)
				case fiber.StatusNotFound:
					return harukiAPIHelper.ErrorNotFound(c, message)
				default:
					return harukiAPIHelper.ErrorInternal(c, message)
				}
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeSessionsDelete, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDeleteSessionFailed, map[string]any{
				"provider": "kratos",
			}))
			return harukiAPIHelper.ErrorInternal(c, "failed to delete session")
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeSessionsDelete, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"sessionTokenID": sessionTokenID,
			"affected":       1,
			"provider":       "kratos",
		})
		return harukiAPIHelper.SuccessResponse[string](c, "session deleted", nil)
	}
}

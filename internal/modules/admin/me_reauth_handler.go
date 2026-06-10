package admin

import (
	"strings"

	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v3"
)

func handleAdminReauth(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, _, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			return respondFiberOrUnauthorized(c, err, "missing user session")
		}

		var payload adminReauthPayload
		if err := c.Bind().Body(&payload); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeReauth, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		password := strings.TrimSpace(payload.Password)
		if password == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeReauth, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingPassword, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "password is required")
		}

		dbUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(userID)).
			Only(c.Context())
		if err != nil {
			reason := adminFailureReasonQueryUserFailed
			if postgresql.IsNotFound(err) {
				reason = adminFailureReasonInvalidUserSession
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeReauth, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(reason, nil))
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorUnauthorized(c, "invalid user session")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to verify account")
		}

		if apiHelper != nil && apiHelper.SessionHandler != nil && apiHelper.SessionHandler.UsesKratosProvider() {
			if dbUser.KratosIdentityID == nil || strings.TrimSpace(*dbUser.KratosIdentityID) == "" {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeReauth, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidUserSession, map[string]any{
					"provider": "kratos",
					"reason":   "identity_not_linked",
				}))
				return harukiAPIHelper.ErrorUnauthorized(c, "invalid user session")
			}

			err := apiHelper.SessionHandler.VerifyKratosPasswordByIdentityID(harukiAPIHelper.WithHTTPRequestMetadata(c.Context(), c.Get("User-Agent"), c.IP()), strings.TrimSpace(*dbUser.KratosIdentityID), payload.Password)
			if err != nil {
				if harukiAPIHelper.IsKratosInvalidCredentialsError(err) || harukiAPIHelper.IsKratosInvalidInputError(err) {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeReauth, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPasswordMismatch, nil))
					return harukiAPIHelper.ErrorForbidden(c, "password mismatch")
				}
				if harukiAPIHelper.IsKratosIdentityUnmappedError(err) {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeReauth, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidUserSession, map[string]any{
						"provider": "kratos",
					}))
					return harukiAPIHelper.ErrorUnauthorized(c, "invalid user session")
				}
				if harukiAPIHelper.IsIdentityProviderUnavailableError(err) {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeReauth, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata("identity_provider_unavailable", nil))
					return harukiAPIHelper.ErrorInternal(c, "failed to verify account")
				}
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeReauth, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPasswordMismatch, map[string]any{
					"provider": "kratos",
				}))
				return harukiAPIHelper.ErrorInternal(c, "failed to verify account")
			}
		} else {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeReauth, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidUserSession, map[string]any{"reason": "managed_identity_required"}))
			return harukiAPIHelper.ErrorUnauthorized(c, "invalid user session")
		}

		if err := markCurrentAdminSessionReauthenticated(c, apiHelper, userID); err != nil {
			reason := adminFailureReasonReauthRequired
			if fiberErr, ok := err.(*fiber.Error); ok {
				switch fiberErr.Code {
				case fiber.StatusUnauthorized:
					reason = adminFailureReasonInvalidUserSession
				case fiber.StatusInternalServerError:
					reason = adminFailureReasonSessionStoreUnavailable
				}
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeReauth, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(reason, nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				switch fiberErr.Code {
				case fiber.StatusUnauthorized:
					return harukiAPIHelper.ErrorUnauthorized(c, "invalid user session")
				case fiber.StatusInternalServerError:
					return harukiAPIHelper.ErrorInternal(c, "failed to save reauthentication state")
				default:
					return harukiAPIHelper.ErrorForbidden(c, "reauthentication required")
				}
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to save reauthentication state")
		}

		resp := adminReauthResponse{ReauthenticatedAt: adminNowUTC()}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionMeReauth, adminAuditTargetTypeUser, userID, harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse(c, "reauthenticated", &resp)
	}
}

package adminoauth

import (
	"strings"

	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	oauth2Module "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/oauth2"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v3"
)

func handleRevokeHydraOAuthClient(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		if clientID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRevoke, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingClientID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id is required")
		}
		_, _, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRevoke, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}
		options, err := parseAdminOAuthClientRevokeOptions(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRevoke, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}
		if !options.RevokeAuthorizations && !options.RevokeTokens {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRevoke, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonNothingToRevoke, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "at least one revoke option must be true")
		}
		if _, err := oauth2Module.GetHydraOAuthClient(c.Context(), clientID); err != nil {
			if oauth2Module.IsHydraNotFoundError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRevoke, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRevoke, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}
		var targetUser *postgresql.User
		if options.TargetUserID != "" {
			targetUser, err = apiHelper.DBManager.DB.User.Query().Where(userSchema.IDEQ(options.TargetUserID)).Select(userSchema.FieldID, userSchema.FieldKratosIdentityID).Only(c.Context())
			if err != nil {
				if postgresql.IsNotFound(err) {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRevoke, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, map[string]any{"targetUserID": options.TargetUserID, "hydraMode": true}))
					return harukiAPIHelper.ErrorNotFound(c, "target user not found")
				}
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRevoke, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTargetUserFailed, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
			}
		}
		if targetUser != nil && options.RevokeTokens {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRevoke, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonRevokeTokensFailed, map[string]any{"hydraMode": true, "targetUserID": targetUser.ID}))
			return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusNotImplemented, "targeted token revocation is unavailable while oauth2 is backed by hydra", nil)
		}
		revokedAuthorizations := 0
		if options.RevokeAuthorizations {
			if targetUser != nil {
				subjects := oauth2Module.HydraSubjectsForUser(targetUser.ID, targetUser.KratosIdentityID)
				if sessions, listErr := oauth2Module.ListHydraConsentSessionsForSubjects(c.Context(), subjects); listErr == nil {
					for _, session := range sessions {
						if session.ConsentRequest.Client.ClientID == clientID {
							revokedAuthorizations++
						}
					}
				}
				if err := oauth2Module.RevokeHydraConsentSessionsForSubjects(c.Context(), subjects, clientID); err != nil {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRevoke, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonRevokeAuthorizationsFailed, map[string]any{"hydraMode": true}))
					return harukiAPIHelper.ErrorInternal(c, "failed to revoke oauth authorizations")
				}
			} else {
				if records, listErr := collectHydraClientAuthorizationRecords(c.Context(), apiHelper, clientID); listErr == nil {
					revokedAuthorizations = len(records)
				}
				if err := oauth2Module.RevokeHydraConsentSessionsByClient(c.Context(), clientID); err != nil {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRevoke, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonRevokeAuthorizationsFailed, map[string]any{"hydraMode": true}))
					return harukiAPIHelper.ErrorInternal(c, "failed to revoke oauth authorizations")
				}
			}
		}
		revokedTokens := 0
		if targetUser == nil && options.RevokeTokens {
			if err := oauth2Module.DeleteHydraOAuthTokensByClientID(c.Context(), clientID); err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRevoke, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonRevokeTokensFailed, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorInternal(c, "failed to revoke oauth tokens")
			}
		}
		var targetUserID *string
		if targetUser != nil {
			target := targetUser.ID
			targetUserID = &target
		}
		resp := adminOAuthClientRevokeResponse{ClientID: clientID, TargetUserID: targetUserID, RevokeAuthorizations: options.RevokeAuthorizations, RevokeTokens: options.RevokeTokens && targetUser == nil, RevokedAuthorizations: revokedAuthorizations, RevokedTokens: revokedTokens}
		metadata := map[string]any{"hydraMode": true, "revokeAuthorizations": options.RevokeAuthorizations, "revokeTokens": options.RevokeTokens && targetUser == nil, "revokedAuthorizations": revokedAuthorizations, "revokedTokens": revokedTokens}
		if targetUser != nil {
			metadata["targetUserID"] = targetUser.ID
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRevoke, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, metadata)
		return harukiAPIHelper.SuccessResponse(c, "oauth client authorizations revoked", &resp)
	}
}

func handleRestoreHydraOAuthClient(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		if clientID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRestore, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingClientID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id is required")
		}
		_, _, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRestore, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}
		updatedClient, err := oauth2Module.SetHydraOAuthClientActive(c.Context(), clientID, true)
		if err != nil {
			if oauth2Module.IsHydraNotFoundError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRestore, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRestore, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonRestoreClientFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to restore oauth client")
		}
		resp := adminOAuthClientRestoreResponse{ClientID: updatedClient.ClientID, Active: oauth2Module.HydraOAuthClientActive(updatedClient)}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRestore, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{"hydraMode": true})
		return harukiAPIHelper.SuccessResponse(c, "oauth client restored", &resp)
	}
}

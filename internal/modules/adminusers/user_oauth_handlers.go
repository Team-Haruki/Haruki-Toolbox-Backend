package adminusers

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	oauth2Module "haruki-suite/internal/modules/oauth2"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"hash/fnv"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func handleListUserOAuthAuthorizations(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetUserID := strings.TrimSpace(c.Params("target_user_id"))
		if targetUserID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthList, adminAuditTargetTypeUser, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingTargetUserID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "target_user_id is required")
		}
		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthList, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}
		targetUser, err := apiHelper.DBManager.DB.User.Query().Where(userSchema.IDEQ(targetUserID)).Select(userSchema.FieldID, userSchema.FieldRole, userSchema.FieldKratosIdentityID, userSchema.FieldName, userSchema.FieldEmail, userSchema.FieldBanned).Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthList, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthList, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTargetUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}
		if err := adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthList, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, map[string]any{"actorRole": actorRole, "targetRole": adminCoreModule.NormalizeRole(string(targetUser.Role))}))
			return adminCoreModule.RespondFiberOrForbidden(c, err, "insufficient permissions")
		}
		includeRevoked, err := parseAdminOAuthIncludeRevoked(c.Query("include_revoked"))
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthList, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidQueryFilters, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid include_revoked filter")
		}
		if includeRevoked {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthList, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidQueryFilters, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorBadRequest(c, "include_revoked is not supported in hydra mode")
		}
		hydraSubjects := oauth2Module.HydraSubjectsForUser(targetUser.ID, targetUser.KratosIdentityID)
		sessions, err := oauth2Module.ListHydraConsentSessionsForSubjects(c.Context(), hydraSubjects)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthList, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryAuthorizationsFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth authorizations")
		}
		items := make([]adminOAuthAuthorizationListItem, 0, len(sessions))
		for _, session := range sessions {
			createdAt := adminNowUTC()
			if session.HandledAt != nil {
				createdAt = session.HandledAt.UTC()
			}
			items = append(items, adminOAuthAuthorizationListItem{
				AuthorizationID:  stableHydraAuthorizationID(session.ConsentRequestID, session.ConsentRequest.Client.ClientID),
				ConsentRequestID: session.ConsentRequestID,
				ClientID:         session.ConsentRequest.Client.ClientID,
				ClientName:       session.ConsentRequest.Client.ClientName,
				ClientType:       oauth2Module.HydraClientTypeFromAuthMethod(session.ConsentRequest.Client.TokenEndpointAuthMethod),
				ClientActive:     true,
				Scopes:           append([]string(nil), session.GrantScope...),
				CreatedAt:        createdAt,
				Revoked:          false,
				TokenStats:       adminOAuthTokenStats{Exact: false},
			})
		}
		resp := adminOAuthAuthorizationListResponse{GeneratedAt: adminNowUTC(), UserID: targetUser.ID, IncludeRevoked: false, Total: len(items), Items: items}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthList, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{"includeRevoked": false, "total": resp.Total, "hydraMode": true})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleRevokeUserOAuth(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetUserID := strings.TrimSpace(c.Params("target_user_id"))
		if targetUserID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingTargetUserID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "target_user_id is required")
		}
		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}
		targetUser, err := apiHelper.DBManager.DB.User.Query().Where(userSchema.IDEQ(targetUserID)).Select(userSchema.FieldID, userSchema.FieldRole, userSchema.FieldKratosIdentityID).Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTargetUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}
		if err := adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, map[string]any{"actorRole": actorRole, "targetRole": adminCoreModule.NormalizeRole(string(targetUser.Role))}))
			return adminCoreModule.RespondFiberOrForbidden(c, err, "insufficient permissions")
		}
		clientID, err := parseAdminRevokeOAuthClientID(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}
		hydraSubjects := oauth2Module.HydraSubjectsForUser(targetUser.ID, targetUser.KratosIdentityID)
		if strings.TrimSpace(clientID) != "" {
			exists, checkErr := oauth2Module.HydraConsentSessionExistsForSubjects(c.Context(), hydraSubjects, clientID)
			if checkErr != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientFailed, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
			}
			if !exists {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, map[string]any{"clientId": clientID, "hydraMode": true}))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
		}
		revokedAuthorizations := 0
		revokedAuthorizationsExact := false
		if sessions, listErr := oauth2Module.ListHydraConsentSessionsForSubjects(c.Context(), hydraSubjects); listErr == nil {
			revokedAuthorizationsExact = true
			for _, session := range sessions {
				if clientID == "" || session.ConsentRequest.Client.ClientID == clientID {
					revokedAuthorizations++
				}
			}
		}
		if err := oauth2Module.RevokeHydraConsentSessionsForSubjects(c.Context(), hydraSubjects, clientID); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonRevokeAuthorizationsFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to revoke oauth authorizations")
		}
		revokedTokens := 0
		revokedTokensExact := false
		var responseClientID *string
		metadata := map[string]any{"revokedAuthorizations": revokedAuthorizations, "revokedAuthorizationsExact": revokedAuthorizationsExact, "revokedTokens": revokedTokens, "revokedTokensExact": revokedTokensExact, "hydraMode": true}
		if clientID != "" {
			responseClientID = &clientID
			metadata["clientId"] = clientID
		}
		resp := adminRevokeOAuthResponse{UserID: targetUser.ID, ClientID: responseClientID, RevokedAuthorizations: revokedAuthorizations, RevokedAuthorizationsExact: &revokedAuthorizationsExact, RevokedTokens: revokedTokens, RevokedTokensExact: &revokedTokensExact}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, metadata)
		return harukiAPIHelper.SuccessResponse(c, "oauth authorizations revoked", &resp)
	}
}

func stableHydraAuthorizationID(consentRequestID string, clientID string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.TrimSpace(consentRequestID)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strings.TrimSpace(clientID)))
	return int(h.Sum32() & 0x7fffffff)
}

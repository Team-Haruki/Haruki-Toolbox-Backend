package adminusers

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	oauth2Module "haruki-suite/internal/modules/oauth2"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/oauthauthorization"
	"haruki-suite/utils/database/postgresql/oauthclient"
	"haruki-suite/utils/database/postgresql/oauthtoken"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"hash/fnv"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
	"strings"
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

		targetUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(targetUserID)).
			Select(userSchema.FieldID, userSchema.FieldRole, userSchema.FieldKratosIdentityID).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthList, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthList, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTargetUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}

		if err := adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthList, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, map[string]any{
				"actorRole":  actorRole,
				"targetRole": adminCoreModule.NormalizeRole(string(targetUser.Role)),
			}))
			return adminCoreModule.RespondFiberOrForbidden(c, err, "insufficient permissions")
		}

		includeRevoked, err := parseAdminOAuthIncludeRevoked(c.Query("include_revoked"))
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthList, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidIncludeRevoked, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid include_revoked filter")
		}
		if oauth2Module.HydraOAuthManagementEnabled() {
			hydraSubject := oauth2Module.PreferredHydraSubject(targetUser.ID, targetUser.KratosIdentityID)
			if includeRevoked {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthList, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidIncludeRevoked, map[string]any{
					"hydraMode": true,
				}))
				return harukiAPIHelper.ErrorBadRequest(c, "include_revoked is not supported in hydra mode")
			}
			sessions, err := oauth2Module.ListHydraConsentSessions(c.Context(), hydraSubject)
			if err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthList, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryAuthorizationsFailed, map[string]any{
					"hydraMode": true,
				}))
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

			resp := adminOAuthAuthorizationListResponse{
				GeneratedAt:    adminNowUTC(),
				UserID:         targetUser.ID,
				IncludeRevoked: false,
				Total:          len(items),
				Items:          items,
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthList, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
				"includeRevoked": false,
				"total":          resp.Total,
				"hydraMode":      true,
			})
			return harukiAPIHelper.SuccessResponse(c, "success", &resp)
		}

		authQuery := apiHelper.DBManager.DB.OAuthAuthorization.Query().
			Where(oauthauthorization.HasUserWith(userSchema.IDEQ(targetUser.ID)))
		if !includeRevoked {
			authQuery = authQuery.Where(oauthauthorization.RevokedEQ(false))
		}

		authorizations, err := authQuery.
			WithClient().
			Order(
				oauthauthorization.ByCreatedAt(sql.OrderDesc()),
				oauthauthorization.ByID(sql.OrderDesc()),
			).
			All(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthList, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryAuthorizationsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth authorizations")
		}

		clientDBIDs := make([]int, 0, len(authorizations))
		for _, auth := range authorizations {
			if auth.Edges.Client == nil {
				continue
			}
			clientDBIDs = append(clientDBIDs, auth.Edges.Client.ID)
		}
		tokenStatsByClientID, err := queryUserOAuthTokenStatsByClients(c.Context(), apiHelper.DBManager.DB, targetUser.ID, clientDBIDs)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthList, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTokenStatsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth token stats")
		}

		items := make([]adminOAuthAuthorizationListItem, 0, len(authorizations))
		for _, auth := range authorizations {
			if auth.Edges.Client == nil {
				continue
			}
			tokenStats := tokenStatsByClientID[auth.Edges.Client.ID]

			items = append(items, adminOAuthAuthorizationListItem{
				AuthorizationID: auth.ID,
				ClientID:        auth.Edges.Client.ClientID,
				ClientName:      auth.Edges.Client.Name,
				ClientType:      auth.Edges.Client.ClientType,
				ClientActive:    auth.Edges.Client.Active,
				Scopes:          append([]string(nil), auth.Scopes...),
				CreatedAt:       auth.CreatedAt.UTC(),
				Revoked:         auth.Revoked,
				TokenStats:      tokenStats,
			})
		}

		resp := adminOAuthAuthorizationListResponse{
			GeneratedAt:    adminNowUTC(),
			UserID:         targetUser.ID,
			IncludeRevoked: includeRevoked,
			Total:          len(items),
			Items:          items,
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthList, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"includeRevoked": includeRevoked,
			"itemCount":      len(items),
		})
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

		targetUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(targetUserID)).
			Select(userSchema.FieldID, userSchema.FieldRole, userSchema.FieldKratosIdentityID).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonTargetUserNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUserID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryTargetUserFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}

		if err := adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPermissionDenied, map[string]any{
				"actorRole":  actorRole,
				"targetRole": adminCoreModule.NormalizeRole(string(targetUser.Role)),
			}))
			return adminCoreModule.RespondFiberOrForbidden(c, err, "insufficient permissions")
		}

		clientID, err := parseAdminRevokeOAuthClientID(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}
		if oauth2Module.HydraOAuthManagementEnabled() {
			hydraSubject := oauth2Module.PreferredHydraSubject(targetUser.ID, targetUser.KratosIdentityID)
			if strings.TrimSpace(clientID) != "" {
				exists, checkErr := oauth2Module.HydraConsentSessionExistsForClient(c.Context(), hydraSubject, clientID)
				if checkErr != nil {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientFailed, map[string]any{
						"hydraMode": true,
					}))
					return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
				}
				if !exists {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, map[string]any{
						"clientId":  clientID,
						"hydraMode": true,
					}))
					return harukiAPIHelper.ErrorNotFound(c, "client not found")
				}
			}
			revokedAuthorizations := 0
			revokedAuthorizationsExact := false
			if sessions, listErr := oauth2Module.ListHydraConsentSessions(c.Context(), hydraSubject); listErr == nil {
				revokedAuthorizationsExact = true
				for _, session := range sessions {
					if clientID == "" || session.ConsentRequest.Client.ClientID == clientID {
						revokedAuthorizations++
					}
				}
			}
			if err := oauth2Module.RevokeHydraConsentSessions(c.Context(), hydraSubject, clientID); err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonRevokeAuthorizationsFailed, map[string]any{
					"hydraMode": true,
				}))
				return harukiAPIHelper.ErrorInternal(c, "failed to revoke oauth authorizations")
			}
			revokedTokens := 0
			revokedTokensExact := false
			var responseClientID *string
			metadata := map[string]any{
				"revokedAuthorizations":      revokedAuthorizations,
				"revokedAuthorizationsExact": revokedAuthorizationsExact,
				"revokedTokens":              revokedTokens,
				"revokedTokensExact":         revokedTokensExact,
				"hydraMode":                  true,
			}
			if clientID != "" {
				responseClientID = &clientID
				metadata["clientId"] = clientID
			}
			resp := adminRevokeOAuthResponse{
				UserID:                     targetUser.ID,
				ClientID:                   responseClientID,
				RevokedAuthorizations:      revokedAuthorizations,
				RevokedAuthorizationsExact: &revokedAuthorizationsExact,
				RevokedTokens:              revokedTokens,
				RevokedTokensExact:         &revokedTokensExact,
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, metadata)
			return harukiAPIHelper.SuccessResponse(c, "oauth authorizations revoked", &resp)
		}

		var client *postgresql.OAuthClient
		if clientID != "" {
			client, err = apiHelper.DBManager.DB.OAuthClient.Query().
				Where(oauthclient.ClientIDEQ(clientID)).
				Only(c.Context())
			if err != nil {
				if postgresql.IsNotFound(err) {
					adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, map[string]any{
						"clientId": clientID,
					}))
					return harukiAPIHelper.ErrorNotFound(c, "client not found")
				}
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientFailed, nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
			}
		}

		tx, err := apiHelper.DBManager.DB.Tx(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonStartTransactionFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to start transaction")
		}

		authUpdate := tx.OAuthAuthorization.Update().
			Where(oauthauthorization.HasUserWith(userSchema.IDEQ(targetUser.ID)))
		tokenUpdate := tx.OAuthToken.Update().
			Where(oauthtoken.HasUserWith(userSchema.IDEQ(targetUser.ID)))
		if client != nil {
			authUpdate = authUpdate.Where(oauthauthorization.HasClientWith(oauthclient.IDEQ(client.ID)))
			tokenUpdate = tokenUpdate.Where(oauthtoken.HasClientWith(oauthclient.IDEQ(client.ID)))
		}

		revokedAuthorizations, err := authUpdate.SetRevoked(true).Save(c.Context())
		if err != nil {
			_ = tx.Rollback()
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonRevokeAuthorizationsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to revoke oauth authorizations")
		}

		revokedTokens, err := tokenUpdate.SetRevoked(true).Save(c.Context())
		if err != nil {
			_ = tx.Rollback()
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonRevokeTokensFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to revoke oauth tokens")
		}

		if err := tx.Commit(); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCommitTransactionFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to commit oauth revoke")
		}

		var responseClientID *string
		metadata := map[string]any{
			"revokedAuthorizations": revokedAuthorizations,
			"revokedTokens":         revokedTokens,
		}
		if clientID != "" {
			responseClientID = &clientID
			metadata["clientId"] = clientID
		}

		revokedAuthorizationsExact := true
		revokedTokensExact := true
		resp := adminRevokeOAuthResponse{
			UserID:                     targetUser.ID,
			ClientID:                   responseClientID,
			RevokedAuthorizations:      revokedAuthorizations,
			RevokedAuthorizationsExact: &revokedAuthorizationsExact,
			RevokedTokens:              revokedTokens,
			RevokedTokensExact:         &revokedTokensExact,
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, metadata)
		return harukiAPIHelper.SuccessResponse(c, "oauth authorizations revoked", &resp)
	}
}

func stableHydraAuthorizationID(consentRequestID, clientID string) int {
	candidate := strings.TrimSpace(consentRequestID)
	if candidate == "" {
		candidate = strings.TrimSpace(clientID)
	}
	if candidate == "" {
		return 1
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(candidate))
	value := int(hasher.Sum32() & 0x7fffffff)
	if value == 0 {
		return 1
	}
	return value
}

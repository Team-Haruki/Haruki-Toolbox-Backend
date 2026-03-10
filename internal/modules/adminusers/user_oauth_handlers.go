package adminusers

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/oauthauthorization"
	"haruki-suite/utils/database/postgresql/oauthclient"
	"haruki-suite/utils/database/postgresql/oauthtoken"
	userSchema "haruki-suite/utils/database/postgresql/user"

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
			Select(userSchema.FieldID, userSchema.FieldRole).
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
			Select(userSchema.FieldID, userSchema.FieldRole).
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

		resp := adminRevokeOAuthResponse{
			UserID:                targetUser.ID,
			ClientID:              responseClientID,
			RevokedAuthorizations: revokedAuthorizations,
			RevokedTokens:         revokedTokens,
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionUserOAuthRevoke, adminAuditTargetTypeUser, targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, metadata)
		return harukiAPIHelper.SuccessResponse(c, "oauth authorizations revoked", &resp)
	}
}

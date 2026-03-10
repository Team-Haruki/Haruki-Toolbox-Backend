package adminoauth

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	oauth2Module "haruki-suite/internal/modules/oauth2"
	platformPagination "haruki-suite/internal/platform/pagination"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/oauthauthorization"
	"haruki-suite/utils/database/postgresql/oauthclient"
	"haruki-suite/utils/database/postgresql/oauthtoken"
	"strings"
	"time"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

func handleListOAuthClients(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		windowHours, err := parseAdminOAuthClientStatsWindowHours(c.Query("hours"))
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientList, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidHours, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid hours")
		}

		includeInactive, err := parseAdminOAuthClientIncludeInactive(c.Query("include_inactive"))
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientList, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidIncludeInactive, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid include_inactive")
		}
		page, pageSize, err := parseAdminOAuthClientListPagination(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientList, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidQueryFilters, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid pagination")
		}

		now := adminNowUTC()
		windowStart := now.Add(-time.Duration(windowHours) * time.Hour)

		clientQuery := apiHelper.DBManager.DB.OAuthClient.Query()
		if !includeInactive {
			clientQuery = clientQuery.Where(oauthclient.ActiveEQ(true))
		}
		total, err := clientQuery.Clone().Count(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientList, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to count oauth clients")
		}

		offset := (page - 1) * pageSize
		clients, err := clientQuery.Order(
			oauthclient.ByCreatedAt(sql.OrderDesc()),
			oauthclient.ByID(sql.OrderDesc()),
		).Limit(pageSize).Offset(offset).All(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientList, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth clients")
		}

		clientDBIDs := make([]int, 0, len(clients))
		for _, client := range clients {
			clientDBIDs = append(clientDBIDs, client.ID)
		}
		usageByClientID, usageErr := queryAdminOAuthClientUsageStatsByClients(c.Context(), apiHelper.DBManager.DB, clientDBIDs, windowStart)
		if usageErr != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientList, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryUsageStatsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client usage")
		}

		items := make([]adminOAuthClientListItem, 0, len(clients))
		for _, client := range clients {
			usage := usageByClientID[client.ID]

			items = append(items, adminOAuthClientListItem{
				ClientID:     client.ClientID,
				Name:         client.Name,
				ClientType:   client.ClientType,
				Active:       client.Active,
				CreatedAt:    client.CreatedAt.UTC(),
				RedirectURIs: append([]string(nil), client.RedirectUris...),
				Scopes:       append([]string(nil), client.Scopes...),
				Usage:        usage,
			})
		}

		resp := adminOAuthClientListResponse{
			GeneratedAt:     now,
			WindowHours:     windowHours,
			WindowStart:     windowStart,
			WindowEnd:       now,
			IncludeInactive: includeInactive,
			Page:            page,
			PageSize:        pageSize,
			Total:           total,
			TotalPages:      platformPagination.CalculateTotalPages(total, pageSize),
			HasMore:         platformPagination.HasMoreByOffset(page, pageSize, total),
			Items:           items,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientList, adminAuditTargetTypeOAuthClient, adminAuditTargetIDAll, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"windowHours":     windowHours,
			"includeInactive": includeInactive,
			"total":           total,
			"itemCount":       len(items),
			"page":            page,
			"pageSize":        pageSize,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleCreateOAuthClient(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		_, _, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientCreate, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		payload, err := parseAdminOAuthClientPayload(c, true)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientCreate, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}

		plainSecret, hashedSecret, err := generateAdminOAuthClientSecret()
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientCreate, adminAuditTargetTypeOAuthClient, payload.ClientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonGenerateClientSecretFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to generate client secret")
		}

		createdClient, err := apiHelper.DBManager.DB.OAuthClient.Create().
			SetClientID(payload.ClientID).
			SetClientSecret(hashedSecret).
			SetName(payload.Name).
			SetClientType(payload.ClientType).
			SetRedirectUris(payload.RedirectURIs).
			SetScopes(payload.Scopes).
			SetActive(true).
			Save(c.Context())
		if err != nil {
			if postgresql.IsConstraintError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientCreate, adminAuditTargetTypeOAuthClient, payload.ClientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientIdConflict, nil))
				return harukiAPIHelper.ErrorBadRequest(c, "clientId already exists")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientCreate, adminAuditTargetTypeOAuthClient, payload.ClientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCreateClientFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to create oauth client")
		}

		resp := adminOAuthClientCreateResponse{
			ClientID:     createdClient.ClientID,
			ClientSecret: plainSecret,
			Name:         createdClient.Name,
			ClientType:   createdClient.ClientType,
			Active:       createdClient.Active,
			RedirectURIs: append([]string(nil), createdClient.RedirectUris...),
			Scopes:       append([]string(nil), createdClient.Scopes...),
			CreatedAt:    createdClient.CreatedAt.UTC(),
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientCreate, adminAuditTargetTypeOAuthClient, createdClient.ClientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"clientType":  createdClient.ClientType,
			"scopeCount":  len(createdClient.Scopes),
			"redirectCnt": len(createdClient.RedirectUris),
		})
		return harukiAPIHelper.SuccessResponse(c, "oauth client created", &resp)
	}
}

func handleUpdateOAuthClientActive(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		if clientID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientActiveUpdate, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingClientID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id is required")
		}

		_, _, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientActiveUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		active, err := parseAdminOAuthClientActiveValue(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientActiveUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}

		dbClient, err := apiHelper.DBManager.DB.OAuthClient.Query().
			Where(oauthclient.ClientIDEQ(clientID)).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientActiveUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientActiveUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}

		updatedClient, err := apiHelper.DBManager.DB.OAuthClient.UpdateOneID(dbClient.ID).
			SetActive(active).
			Save(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientActiveUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateClientFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to update oauth client")
		}

		resp := adminOAuthClientActiveResponse{
			ClientID: updatedClient.ClientID,
			Active:   updatedClient.Active,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientActiveUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"active": active,
		})
		return harukiAPIHelper.SuccessResponse(c, "oauth client status updated", &resp)
	}
}

func handleUpdateOAuthClient(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		if clientID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientUpdate, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingClientID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id is required")
		}

		_, _, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		payload, err := parseAdminOAuthClientPayload(c, false)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}

		dbClient, err := apiHelper.DBManager.DB.OAuthClient.Query().
			Where(oauthclient.ClientIDEQ(clientID)).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}

		updatedClient, err := apiHelper.DBManager.DB.OAuthClient.UpdateOneID(dbClient.ID).
			SetName(payload.Name).
			SetClientType(payload.ClientType).
			SetRedirectUris(payload.RedirectURIs).
			SetScopes(payload.Scopes).
			Save(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateClientFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to update oauth client")
		}

		resp := adminOAuthClientUpdateResponse{
			ClientID:     updatedClient.ClientID,
			Name:         updatedClient.Name,
			ClientType:   updatedClient.ClientType,
			Active:       updatedClient.Active,
			RedirectURIs: append([]string(nil), updatedClient.RedirectUris...),
			Scopes:       append([]string(nil), updatedClient.Scopes...),
			CreatedAt:    updatedClient.CreatedAt.UTC(),
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"clientType":  updatedClient.ClientType,
			"scopeCount":  len(updatedClient.Scopes),
			"redirectCnt": len(updatedClient.RedirectUris),
		})
		return harukiAPIHelper.SuccessResponse(c, "oauth client updated", &resp)
	}
}

func handleRotateOAuthClientSecret(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		if clientID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRotateSecret, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingClientID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id is required")
		}

		_, _, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRotateSecret, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		dbClient, err := apiHelper.DBManager.DB.OAuthClient.Query().
			Where(oauthclient.ClientIDEQ(clientID)).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRotateSecret, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRotateSecret, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}

		plainSecret, hashedSecret, err := generateAdminOAuthClientSecret()
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRotateSecret, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonGenerateClientSecretFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to generate client secret")
		}

		if _, err := apiHelper.DBManager.DB.OAuthClient.UpdateOneID(dbClient.ID).
			SetClientSecret(hashedSecret).
			Save(c.Context()); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRotateSecret, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateClientSecretFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to rotate oauth client secret")
		}

		resp := adminOAuthClientRotateSecretResponse{
			ClientID:     dbClient.ClientID,
			ClientSecret: plainSecret,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRotateSecret, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse(c, "oauth client secret rotated", &resp)
	}
}

func handleDeleteOAuthClient(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		if clientID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientDelete, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingClientID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id is required")
		}

		_, _, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}

		options, err := parseAdminOAuthClientDeleteOptions(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}

		dbClient, err := apiHelper.DBManager.DB.OAuthClient.Query().
			Where(oauthclient.ClientIDEQ(clientID)).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}

		tx, err := apiHelper.DBManager.DB.Tx(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonStartTransactionFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to start transaction")
		}

		deletedAuthorizations := 0
		if options.DeleteAuthorizations {
			deletedAuthorizations, err = tx.OAuthAuthorization.Delete().
				Where(oauthauthorization.HasClientWith(oauthclient.IDEQ(dbClient.ID))).
				Exec(c.Context())
			if err != nil {
				_ = tx.Rollback()
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDeleteAuthorizationsFailed, nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to delete oauth authorizations")
			}
		}

		deletedTokens := 0
		if options.DeleteTokens {
			deletedTokens, err = tx.OAuthToken.Delete().
				Where(oauthtoken.HasClientWith(oauthclient.IDEQ(dbClient.ID))).
				Exec(c.Context())
			if err != nil {
				_ = tx.Rollback()
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDeleteTokensFailed, nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to delete oauth tokens")
			}
		}

		if err := tx.OAuthClient.DeleteOneID(dbClient.ID).Exec(c.Context()); err != nil {
			_ = tx.Rollback()
			if postgresql.IsConstraintError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientHasDependencies, map[string]any{
					"deleteAuthorizations": options.DeleteAuthorizations,
					"deleteTokens":         options.DeleteTokens,
				}))
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "oauth client has dependent authorizations or tokens", nil)
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDeleteClientFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to delete oauth client")
		}

		if err := tx.Commit(); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCommitTransactionFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to commit oauth client delete")
		}

		resp := adminOAuthClientDeleteResponse{
			ClientID:              dbClient.ClientID,
			DeleteAuthorizations:  options.DeleteAuthorizations,
			DeleteTokens:          options.DeleteTokens,
			DeletedAuthorizations: deletedAuthorizations,
			DeletedTokens:         deletedTokens,
			RevokeAuthorizations:  options.DeleteAuthorizations,
			RevokeTokens:          options.DeleteTokens,
			RevokedAuthorizations: deletedAuthorizations,
			RevokedTokens:         deletedTokens,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"deleteAuthorizations":  options.DeleteAuthorizations,
			"deleteTokens":          options.DeleteTokens,
			"deletedAuthorizations": deletedAuthorizations,
			"deletedTokens":         deletedTokens,
		})
		return harukiAPIHelper.SuccessResponse(c, "oauth client deleted", &resp)
	}
}

func RegisterAdminOAuthClientRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	adminGroup := adminCoreModule.AdminRootGroup(apiHelper)
	oauthClients := adminGroup.Group("/oauth-clients", adminCoreModule.RequireAdmin(apiHelper))
	if oauth2Module.HydraOAuthManagementEnabled() {
		unsupported := handleHydraBackedOAuthClientAdminUnavailable()
		oauthClients.Post("", adminCoreModule.RequireSuperAdmin(apiHelper), unsupported)
		oauthClients.Get("", unsupported)
		oauthClients.Get("/:client_id/authorizations", unsupported)
		oauthClients.Get("/:client_id/statistics", unsupported)
		oauthClients.Get("/:client_id/audit-logs", unsupported)
		oauthClients.Get("/:client_id/audit-summary", unsupported)
		oauthClients.Post("/:client_id/revoke", adminCoreModule.RequireSuperAdmin(apiHelper), unsupported)
		oauthClients.Post("/:client_id/restore", adminCoreModule.RequireSuperAdmin(apiHelper), unsupported)
		oauthClients.Put("/:client_id", adminCoreModule.RequireSuperAdmin(apiHelper), unsupported)
		oauthClients.Put("/:client_id/active", adminCoreModule.RequireSuperAdmin(apiHelper), unsupported)
		oauthClients.Post("/:client_id/rotate-secret", adminCoreModule.RequireSuperAdmin(apiHelper), unsupported)
		oauthClients.Delete("/:client_id", adminCoreModule.RequireSuperAdmin(apiHelper), unsupported)
		return
	}
	oauthClients.Post("", adminCoreModule.RequireSuperAdmin(apiHelper), handleCreateOAuthClient(apiHelper))
	oauthClients.Get("", handleListOAuthClients(apiHelper))
	oauthClients.Get("/:client_id/authorizations", handleListOAuthClientAuthorizations(apiHelper))
	oauthClients.Get("/:client_id/statistics", handleGetOAuthClientStatistics(apiHelper))
	oauthClients.Get("/:client_id/audit-logs", handleListOAuthClientAuditLogs(apiHelper))
	oauthClients.Get("/:client_id/audit-summary", handleGetOAuthClientAuditSummary(apiHelper))
	oauthClients.Post("/:client_id/revoke", adminCoreModule.RequireSuperAdmin(apiHelper), handleRevokeOAuthClient(apiHelper))
	oauthClients.Post("/:client_id/restore", adminCoreModule.RequireSuperAdmin(apiHelper), handleRestoreOAuthClient(apiHelper))
	oauthClients.Put("/:client_id", adminCoreModule.RequireSuperAdmin(apiHelper), handleUpdateOAuthClient(apiHelper))
	oauthClients.Put("/:client_id/active", adminCoreModule.RequireSuperAdmin(apiHelper), handleUpdateOAuthClientActive(apiHelper))
	oauthClients.Post("/:client_id/rotate-secret", adminCoreModule.RequireSuperAdmin(apiHelper), handleRotateOAuthClientSecret(apiHelper))
	oauthClients.Delete("/:client_id", adminCoreModule.RequireSuperAdmin(apiHelper), handleDeleteOAuthClient(apiHelper))
}

func handleHydraBackedOAuthClientAdminUnavailable() fiber.Handler {
	return func(c fiber.Ctx) error {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusNotImplemented, "oauth client admin api is unavailable while oauth2 is backed by hydra", nil)
	}
}

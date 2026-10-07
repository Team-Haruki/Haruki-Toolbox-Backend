package adminoauth

import (
	"errors"
	"sort"
	"strings"
	"time"

	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	oauth2Module "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/oauth2"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	platformPagination "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/pagination"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
)

func handleListHydraOAuthClients(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig) fiber.Handler {
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
		hydraClients, err := oauth2Module.ListHydraOAuthClients(c.Context(), hydraConfig)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientList, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientsFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth clients")
		}

		clients := make([]oauth2Module.HydraOAuthClient, 0, len(hydraClients))
		for _, client := range hydraClients {
			if !includeInactive && !oauth2Module.HydraOAuthClientActive(&client) {
				continue
			}
			clients = append(clients, client)
		}
		sort.Slice(clients, func(i, j int) bool {
			left := hydraClientCreatedAt(&clients[i])
			right := hydraClientCreatedAt(&clients[j])
			if left.Equal(right) {
				return clients[i].ClientID > clients[j].ClientID
			}
			return left.After(right)
		})

		total := len(clients)
		offset := (page - 1) * pageSize
		if offset > total {
			offset = total
		}
		end := offset + pageSize
		if end > total {
			end = total
		}
		pageClients := clients[offset:end]
		items := make([]adminOAuthClientListItem, 0, len(pageClients))
		for _, client := range pageClients {
			items = append(items, adminOAuthClientListItem{
				ClientID:               client.ClientID,
				Name:                   strings.TrimSpace(client.ClientName),
				ClientType:             oauth2Module.HydraClientTypeFromAuthMethod(client.TokenEndpointAuthMethod),
				Active:                 oauth2Module.HydraOAuthClientActive(&client),
				CreatedAt:              hydraClientCreatedAt(&client),
				RedirectURIs:           append([]string(nil), client.RedirectURIs...),
				PostLogoutRedirectURIs: append([]string(nil), client.PostLogoutRedirectURIs...),
				Scopes:                 append([]string(nil), oauth2Module.HydraOAuthClientScopes(&client)...),
				Usage:                  adminOAuthClientUsageStats{},
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
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientList, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"hydraMode":       true,
			"windowHours":     windowHours,
			"includeInactive": includeInactive,
			"total":           total,
		})
		return harukiAPIHelper.Responses.SuccessResponse(c, "success", &resp)
	}
}

func handleCreateHydraOAuthClient(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig) fiber.Handler {
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
		plainSecret := ""
		if payload.ClientType == "confidential" {
			plainSecret, err = harukiOAuth2.GenerateRandomToken(32)
			if err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientCreate, adminAuditTargetTypeOAuthClient, payload.ClientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCreateClientFailed, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorInternal(c, "failed to create oauth client")
			}
		}
		createdClient, err := oauth2Module.CreateHydraOAuthClient(c.Context(), hydraConfig, oauth2Module.HydraOAuthClientUpsertInput{
			ClientID:               payload.ClientID,
			ClientSecret:           plainSecret,
			ClientName:             payload.Name,
			ClientType:             payload.ClientType,
			RedirectURIs:           payload.RedirectURIs,
			PostLogoutRedirectURIs: payload.PostLogoutRedirectURIs,
			Scopes:                 payload.Scopes,
			Active:                 true,
		})
		if err != nil {
			if oauth2Module.IsHydraConflictError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientCreate, adminAuditTargetTypeOAuthClient, payload.ClientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientIdConflict, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorBadRequest(c, "clientId already exists")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientCreate, adminAuditTargetTypeOAuthClient, payload.ClientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCreateClientFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to create oauth client")
		}
		resp := adminOAuthClientCreateResponse{
			ClientID:               createdClient.ClientID,
			ClientSecret:           plainSecret,
			Name:                   strings.TrimSpace(createdClient.ClientName),
			ClientType:             oauth2Module.HydraClientTypeFromAuthMethod(createdClient.TokenEndpointAuthMethod),
			Active:                 oauth2Module.HydraOAuthClientActive(createdClient),
			RedirectURIs:           append([]string(nil), createdClient.RedirectURIs...),
			PostLogoutRedirectURIs: append([]string(nil), createdClient.PostLogoutRedirectURIs...),
			Scopes:                 append([]string(nil), oauth2Module.HydraOAuthClientScopes(createdClient)...),
			CreatedAt:              hydraClientCreatedAt(createdClient),
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientCreate, adminAuditTargetTypeOAuthClient, createdClient.ClientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"hydraMode":             true,
			"clientType":            resp.ClientType,
			"scopeCount":            len(resp.Scopes),
			"redirectCnt":           len(resp.RedirectURIs),
			"postLogoutRedirectCnt": len(resp.PostLogoutRedirectURIs),
		})
		return harukiAPIHelper.Responses.SuccessResponse(c, "oauth client created", &resp)
	}
}

func handleUpdateHydraOAuthClientActive(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		if clientID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientActiveUpdate, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingClientID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id is required")
		}
		actorUserID, actorRole, err := adminCoreModule.CurrentAdminActor(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientActiveUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingUserSession, nil))
			return adminCoreModule.RespondFiberOrUnauthorized(c, err, "missing user session")
		}
		active, err := parseAdminOAuthClientActiveValue(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientActiveUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
		}
		updatedClient, err := oauth2Module.SetHydraOAuthClientActive(c.Context(), hydraConfig, clientID, active)
		if err != nil {
			if oauth2Module.IsHydraNotFoundError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientActiveUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientActiveUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateClientFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to update oauth client")
		}
		resp := adminOAuthClientActiveResponse{
			ClientID:           updatedClient.ClientID,
			Active:             oauth2Module.HydraOAuthClientActive(updatedClient),
			FailedSubjects:     []string{},
			RevocationComplete: true,
		}
		metadata := map[string]any{"hydraMode": true, "active": active}
		message := "oauth client status updated"
		if !active {
			// Disabling must cut existing access, not only flip the metadata flag. The
			// client is already disabled here, so revocation failures are reported in a
			// 200 and the admin can finish the job with revoke-all.
			revocation, enumerateErr := revokeHydraClientGrantsPerSubject(c.Context(), apiHelper, hydraConfig, clientID)
			if enumerateErr != nil {
				harukiLogger.Warnf("Failed to list grants of disabled oauth client %s: %v", clientID, enumerateErr)
				metadata["queryAuthorizationsFailed"] = true
			}
			// Access tokens only: refresh tokens die with the consent sessions revoked
			// above. This also reaches subjects the enumeration cannot find.
			tokensErr := oauth2Module.DeleteHydraOAuthTokensByClientID(c.Context(), hydraConfig, clientID)
			if tokensErr != nil {
				harukiLogger.Warnf("Failed to delete access tokens of disabled oauth client %s: %v", clientID, tokensErr)
				metadata["revokeTokensFailed"] = true
			}
			resp.RevokedSubjects = revocation.revoked
			resp.FailedSubjects = revocation.failedSubjectsVisibleTo(actorUserID, actorRole)
			resp.RevocationComplete = enumerateErr == nil && revocation.complete() && tokensErr == nil
			metadata["revokedSubjects"] = revocation.revoked
			metadata["failedSubjects"] = len(revocation.failed)
			metadata["revocationComplete"] = resp.RevocationComplete
			if !resp.RevocationComplete {
				message = "oauth client disabled, but some grants could not be revoked"
			}
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientActiveUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, metadata)
		return harukiAPIHelper.Responses.SuccessResponse(c, message, &resp)
	}
}

func handleUpdateHydraOAuthClient(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig) fiber.Handler {
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
		currentClient, err := oauth2Module.GetHydraOAuthClient(c.Context(), hydraConfig, clientID)
		if err != nil {
			if oauth2Module.IsHydraNotFoundError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}
		if payload.PostLogoutRedirectURIs == nil {
			if err := ensureAdminOAuthClientKeptPostLogoutRedirectURIsMatch(currentClient.PostLogoutRedirectURIs, payload.RedirectURIs); err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, map[string]any{"hydraMode": true}))
				return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid request payload")
			}
		}
		plainSecret := ""
		if oauth2Module.HydraOAuthClientSwitchesToConfidential(currentClient, payload.ClientType) {
			// Hydra accepts the auth-method change without a secret and the client could then
			// never authenticate, so the secret goes into the same patch.
			plainSecret, err = harukiOAuth2.GenerateRandomToken(32)
			if err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonGenerateClientSecretFailed, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorInternal(c, "failed to update oauth client")
			}
		}
		// GrantTypes stays nil: the admin payload does not carry grant types, so the
		// registered grant_types and response_types are kept.
		updatedClient, err := oauth2Module.UpdateHydraOAuthClient(c.Context(), hydraConfig, currentClient, oauth2Module.HydraOAuthClientUpsertInput{
			ClientID:               clientID,
			ClientSecret:           plainSecret,
			ClientName:             payload.Name,
			ClientType:             payload.ClientType,
			RedirectURIs:           payload.RedirectURIs,
			PostLogoutRedirectURIs: payload.PostLogoutRedirectURIs,
			Scopes:                 payload.Scopes,
		})
		if err != nil {
			if errors.Is(err, oauth2Module.ErrHydraPostLogoutRequiresRedirectURIs) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorBadRequest(c, "postLogoutRedirectUris requires redirectUris")
			}
			// ErrHydraClientSecretRequired stays a 500: this handler generates the secret
			// whenever the switch needs one, so that error means a bug, not bad input.
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateClientFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to update oauth client")
		}
		resp := adminOAuthClientUpdateResponse{
			ClientID:               updatedClient.ClientID,
			ClientSecret:           plainSecret,
			Name:                   strings.TrimSpace(updatedClient.ClientName),
			ClientType:             oauth2Module.HydraClientTypeFromAuthMethod(updatedClient.TokenEndpointAuthMethod),
			Active:                 oauth2Module.HydraOAuthClientActive(updatedClient),
			RedirectURIs:           append([]string(nil), updatedClient.RedirectURIs...),
			PostLogoutRedirectURIs: append([]string(nil), updatedClient.PostLogoutRedirectURIs...),
			Scopes:                 append([]string(nil), oauth2Module.HydraOAuthClientScopes(updatedClient)...),
			CreatedAt:              hydraClientCreatedAt(updatedClient),
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"hydraMode":             true,
			"clientType":            resp.ClientType,
			"scopeCount":            len(resp.Scopes),
			"redirectCnt":           len(resp.RedirectURIs),
			"postLogoutRedirectCnt": len(resp.PostLogoutRedirectURIs),
			"clientSecretIssued":    plainSecret != "",
		})
		return harukiAPIHelper.Responses.SuccessResponse(c, "oauth client updated", &resp)
	}
}

func handleRotateHydraOAuthClientSecret(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig) fiber.Handler {
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
		plainSecret, err := harukiOAuth2.GenerateRandomToken(32)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRotateSecret, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonGenerateClientSecretFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to rotate oauth client secret")
		}
		if _, err := oauth2Module.RotateHydraOAuthClientSecret(c.Context(), hydraConfig, clientID, plainSecret); err != nil {
			if oauth2Module.IsHydraNotFoundError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRotateSecret, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			if errors.Is(err, oauth2Module.ErrHydraPublicClientHasNoSecret) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRotateSecret, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPublicClientHasNoSecret, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.Responses.UpdatedDataResponse(c, fiber.StatusBadRequest, "public clients have no secret to rotate", &adminOAuthClientErrorData{Code: adminOAuthClientErrorCodePublicClientHasNoSecret})
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRotateSecret, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateClientSecretFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to rotate oauth client secret")
		}
		resp := adminOAuthClientRotateSecretResponse{ClientID: clientID, ClientSecret: plainSecret}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRotateSecret, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{"hydraMode": true})
		return harukiAPIHelper.Responses.SuccessResponse(c, "oauth client secret rotated", &resp)
	}
}

func handleDeleteHydraOAuthClient(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig) fiber.Handler {
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
		if _, err := oauth2Module.GetHydraOAuthClient(c.Context(), hydraConfig, clientID); err != nil {
			if oauth2Module.IsHydraNotFoundError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}
		deletedAuthorizations := 0
		if options.DeleteAuthorizations {
			// Nothing to revoke first: deleting the client cascades to its consent
			// sessions and tokens in Hydra. The grants are only counted.
			if records, listErr := collectHydraClientAuthorizationRecords(c.Context(), apiHelper, hydraConfig, clientID); listErr == nil {
				deletedAuthorizations = len(records)
			}
		}
		deletedTokens := 0
		if options.DeleteTokens {
			if err := oauth2Module.DeleteHydraOAuthTokensByClientID(c.Context(), hydraConfig, clientID); err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDeleteTokensFailed, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorInternal(c, "failed to delete oauth tokens")
			}
		}
		if err := oauth2Module.DeleteHydraOAuthClient(c.Context(), hydraConfig, clientID); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDeleteClientFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to delete oauth client")
		}
		resp := adminOAuthClientDeleteResponse{
			ClientID:              clientID,
			DeleteAuthorizations:  options.DeleteAuthorizations,
			DeleteTokens:          options.DeleteTokens,
			DeletedAuthorizations: deletedAuthorizations,
			DeletedTokens:         deletedTokens,
			RevokeAuthorizations:  options.DeleteAuthorizations,
			RevokeTokens:          options.DeleteTokens,
			RevokedAuthorizations: deletedAuthorizations,
			RevokedTokens:         deletedTokens,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{"hydraMode": true, "deleteAuthorizations": options.DeleteAuthorizations, "deleteTokens": options.DeleteTokens, "deletedAuthorizations": deletedAuthorizations, "deletedTokens": deletedTokens})
		return harukiAPIHelper.Responses.SuccessResponse(c, "oauth client deleted", &resp)
	}
}

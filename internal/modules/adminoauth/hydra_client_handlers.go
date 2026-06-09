package adminoauth

import (
	"sort"
	"strings"
	"time"

	adminCoreModule "haruki-suite/internal/modules/admincore"
	oauth2Module "haruki-suite/internal/modules/oauth2"
	platformPagination "haruki-suite/internal/platform/pagination"
	harukiAPIHelper "haruki-suite/utils/api"
	harukiOAuth2 "haruki-suite/utils/oauth2"

	"github.com/gofiber/fiber/v3"
)

func handleListHydraOAuthClients(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
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
		hydraClients, err := oauth2Module.ListHydraOAuthClients(c.Context())
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
				ClientID:     client.ClientID,
				Name:         strings.TrimSpace(client.ClientName),
				ClientType:   oauth2Module.HydraClientTypeFromAuthMethod(client.TokenEndpointAuthMethod),
				Active:       oauth2Module.HydraOAuthClientActive(&client),
				CreatedAt:    hydraClientCreatedAt(&client),
				RedirectURIs: append([]string(nil), client.RedirectURIs...),
				Scopes:       append([]string(nil), oauth2Module.HydraOAuthClientScopes(&client)...),
				Usage:        adminOAuthClientUsageStats{},
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
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleCreateHydraOAuthClient(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
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
		createdClient, err := oauth2Module.CreateHydraOAuthClient(c.Context(), oauth2Module.HydraOAuthClientUpsertInput{
			ClientID:     payload.ClientID,
			ClientSecret: plainSecret,
			ClientName:   payload.Name,
			ClientType:   payload.ClientType,
			RedirectURIs: payload.RedirectURIs,
			Scopes:       payload.Scopes,
			Active:       true,
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
			ClientID:     createdClient.ClientID,
			ClientSecret: plainSecret,
			Name:         strings.TrimSpace(createdClient.ClientName),
			ClientType:   oauth2Module.HydraClientTypeFromAuthMethod(createdClient.TokenEndpointAuthMethod),
			Active:       oauth2Module.HydraOAuthClientActive(createdClient),
			RedirectURIs: append([]string(nil), createdClient.RedirectURIs...),
			Scopes:       append([]string(nil), oauth2Module.HydraOAuthClientScopes(createdClient)...),
			CreatedAt:    hydraClientCreatedAt(createdClient),
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientCreate, adminAuditTargetTypeOAuthClient, createdClient.ClientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"hydraMode":   true,
			"clientType":  resp.ClientType,
			"scopeCount":  len(resp.Scopes),
			"redirectCnt": len(resp.RedirectURIs),
		})
		return harukiAPIHelper.SuccessResponse(c, "oauth client created", &resp)
	}
}

func handleUpdateHydraOAuthClientActive(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
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
		updatedClient, err := oauth2Module.SetHydraOAuthClientActive(c.Context(), clientID, active)
		if err != nil {
			if oauth2Module.IsHydraNotFoundError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientActiveUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientActiveUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateClientFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to update oauth client")
		}
		resp := adminOAuthClientActiveResponse{ClientID: updatedClient.ClientID, Active: oauth2Module.HydraOAuthClientActive(updatedClient)}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientActiveUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{"hydraMode": true, "active": active})
		return harukiAPIHelper.SuccessResponse(c, "oauth client status updated", &resp)
	}
}

func handleUpdateHydraOAuthClient(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
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
		currentClient, err := oauth2Module.GetHydraOAuthClient(c.Context(), clientID)
		if err != nil {
			if oauth2Module.IsHydraNotFoundError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}
		updatedClient, err := oauth2Module.UpdateHydraOAuthClient(c.Context(), clientID, oauth2Module.HydraOAuthClientUpsertInput{
			ClientID:     clientID,
			ClientName:   payload.Name,
			ClientType:   payload.ClientType,
			RedirectURIs: payload.RedirectURIs,
			Scopes:       payload.Scopes,
			Active:       oauth2Module.HydraOAuthClientActive(currentClient),
		})
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateClientFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to update oauth client")
		}
		resp := adminOAuthClientUpdateResponse{
			ClientID:     updatedClient.ClientID,
			Name:         strings.TrimSpace(updatedClient.ClientName),
			ClientType:   oauth2Module.HydraClientTypeFromAuthMethod(updatedClient.TokenEndpointAuthMethod),
			Active:       oauth2Module.HydraOAuthClientActive(updatedClient),
			RedirectURIs: append([]string(nil), updatedClient.RedirectURIs...),
			Scopes:       append([]string(nil), oauth2Module.HydraOAuthClientScopes(updatedClient)...),
			CreatedAt:    hydraClientCreatedAt(updatedClient),
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{"hydraMode": true, "scopeCount": len(resp.Scopes), "redirectCnt": len(resp.RedirectURIs)})
		return harukiAPIHelper.SuccessResponse(c, "oauth client updated", &resp)
	}
}

func handleRotateHydraOAuthClientSecret(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
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
		if _, err := oauth2Module.RotateHydraOAuthClientSecret(c.Context(), clientID, plainSecret); err != nil {
			if oauth2Module.IsHydraNotFoundError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRotateSecret, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRotateSecret, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateClientSecretFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to rotate oauth client secret")
		}
		resp := adminOAuthClientRotateSecretResponse{ClientID: clientID, ClientSecret: plainSecret}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientRotateSecret, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{"hydraMode": true})
		return harukiAPIHelper.SuccessResponse(c, "oauth client secret rotated", &resp)
	}
}

func handleDeleteHydraOAuthClient(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
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
		if _, err := oauth2Module.GetHydraOAuthClient(c.Context(), clientID); err != nil {
			if oauth2Module.IsHydraNotFoundError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}
		deletedAuthorizations := 0
		if options.DeleteAuthorizations {
			if records, listErr := collectHydraClientAuthorizationRecords(c.Context(), apiHelper, clientID); listErr == nil {
				deletedAuthorizations = len(records)
			}
			if err := oauth2Module.RevokeHydraConsentSessionsByClient(c.Context(), clientID); err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDeleteAuthorizationsFailed, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorInternal(c, "failed to delete oauth authorizations")
			}
		}
		deletedTokens := 0
		if options.DeleteTokens {
			if err := oauth2Module.DeleteHydraOAuthTokensByClientID(c.Context(), clientID); err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDeleteTokensFailed, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorInternal(c, "failed to delete oauth tokens")
			}
		}
		if err := oauth2Module.DeleteHydraOAuthClient(c.Context(), clientID); err != nil {
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
		return harukiAPIHelper.SuccessResponse(c, "oauth client deleted", &resp)
	}
}

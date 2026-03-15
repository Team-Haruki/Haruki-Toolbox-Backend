package adminoauth

import (
	"context"

	adminCoreModule "haruki-suite/internal/modules/admincore"
	oauth2Module "haruki-suite/internal/modules/oauth2"
	platformPagination "haruki-suite/internal/platform/pagination"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/systemlog"
	userSchema "haruki-suite/utils/database/postgresql/user"
	harukiOAuth2 "haruki-suite/utils/oauth2"
	"hash/fnv"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"golang.org/x/sync/errgroup"
)

type hydraClientAuthorizationRecord struct {
	User    *postgresql.User
	Session oauth2Module.HydraConsentSession
	Subject string
}

const hydraClientAuthorizationScanWorkers = 8

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

func handleListHydraOAuthClientAuthorizations(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		if clientID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuthorizationsList, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingClientID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id is required")
		}
		filters, err := parseAdminOAuthClientAuthorizationsFilters(c)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuthorizationsList, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidQueryFilters, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid query filters")
		}
		if filters.IncludeRevoked {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuthorizationsList, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidQueryFilters, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorBadRequest(c, "include_revoked is not supported in hydra mode")
		}
		hydraClient, err := oauth2Module.GetHydraOAuthClient(c.Context(), clientID)
		if err != nil {
			if oauth2Module.IsHydraNotFoundError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuthorizationsList, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuthorizationsList, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}
		records, err := collectHydraClientAuthorizationRecords(c.Context(), apiHelper, clientID)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuthorizationsList, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryAuthorizationsFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth authorizations")
		}
		sort.Slice(records, func(i, j int) bool {
			left := hydraConsentHandledAt(records[i].Session)
			right := hydraConsentHandledAt(records[j].Session)
			if left.Equal(right) {
				return stableAdminHydraAuthorizationID(records[i].Session.ConsentRequestID, clientID) > stableAdminHydraAuthorizationID(records[j].Session.ConsentRequestID, clientID)
			}
			return left.After(right)
		})
		total := len(records)
		offset := (filters.Page - 1) * filters.PageSize
		if offset > total {
			offset = total
		}
		end := offset + filters.PageSize
		if end > total {
			end = total
		}
		pageRecords := records[offset:end]
		items := make([]adminOAuthClientAuthorizationListItem, 0, len(pageRecords))
		for _, record := range pageRecords {
			items = append(items, adminOAuthClientAuthorizationListItem{
				AuthorizationID: stableAdminHydraAuthorizationID(record.Session.ConsentRequestID, clientID),
				User: adminOAuthClientAuthorizationUser{
					UserID: record.User.ID,
					Name:   record.User.Name,
					Email:  record.User.Email,
					Role:   adminCoreModule.NormalizeRole(string(record.User.Role)),
					Banned: record.User.Banned,
				},
				Scopes:     append([]string(nil), record.Session.GrantScope...),
				CreatedAt:  hydraConsentHandledAt(record.Session),
				Revoked:    false,
				TokenStats: adminOAuthTokenStats{},
			})
		}
		resp := adminOAuthClientAuthorizationsResponse{
			GeneratedAt:    adminNowUTC(),
			ClientID:       hydraClient.ClientID,
			ClientName:     strings.TrimSpace(hydraClient.ClientName),
			IncludeRevoked: false,
			Page:           filters.Page,
			PageSize:       filters.PageSize,
			Total:          total,
			TotalPages:     platformPagination.CalculateTotalPages(total, filters.PageSize),
			HasMore:        platformPagination.HasMoreByOffset(filters.Page, filters.PageSize, total),
			Items:          items,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuthorizationsList, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{"hydraMode": true, "includeRevoked": false, "total": total})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleGetHydraOAuthClientStatistics(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		if clientID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientStatisticsQuery, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingClientID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id is required")
		}
		filters, err := parseAdminOAuthClientStatisticsFilters(c, adminNow())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientStatisticsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidQueryFilters, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid query filters")
		}
		hydraClient, err := oauth2Module.GetHydraOAuthClient(c.Context(), clientID)
		if err != nil {
			if oauth2Module.IsHydraNotFoundError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientStatisticsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientStatisticsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}
		records, err := collectHydraClientAuthorizationRecords(c.Context(), apiHelper, clientID)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientStatisticsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryAuthorizationsFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client statistics")
		}
		authorizationTimes := make([]time.Time, 0, len(records))
		authorizationCreatedInRange := 0
		for _, record := range records {
			handledAt := hydraConsentHandledAt(record.Session)
			authorizationTimes = append(authorizationTimes, handledAt)
			if !handledAt.Before(filters.From.UTC()) && !handledAt.After(filters.To.UTC()) {
				authorizationCreatedInRange++
			}
		}
		resp := adminOAuthClientStatisticsResponse{
			GeneratedAt: adminNowUTC(),
			ClientID:    hydraClient.ClientID,
			ClientName:  strings.TrimSpace(hydraClient.ClientName),
			ClientType:  oauth2Module.HydraClientTypeFromAuthMethod(hydraClient.TokenEndpointAuthMethod),
			Active:      oauth2Module.HydraOAuthClientActive(hydraClient),
			From:        filters.From.UTC(),
			To:          filters.To.UTC(),
			Bucket:      filters.Bucket,
			Summary: adminOAuthClientStatisticsSummary{
				AuthorizationTotal:          len(records),
				AuthorizationActive:         len(records),
				AuthorizationRevoked:        0,
				AuthorizationCreatedInRange: authorizationCreatedInRange,
				TokenTotal:                  0,
				TokenActive:                 0,
				TokenRevoked:                0,
				TokenIssuedInRange:          0,
			},
			Trend: buildAdminOAuthClientTrendPoints(filters.From.UTC(), filters.To.UTC(), filters.Bucket, authorizationTimes, nil),
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientStatisticsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{"hydraMode": true, "authorizationTotal": len(records)})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

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

func handleListHydraOAuthClientAuditLogs(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		if clientID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditLogsQuery, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingClientID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id is required")
		}
		filters, err := parseAdminOAuthClientAuditFilters(c, adminNow())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditLogsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidQueryFilters, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid query filters")
		}
		hydraClient, err := oauth2Module.GetHydraOAuthClient(c.Context(), clientID)
		if err != nil {
			if oauth2Module.IsHydraNotFoundError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditLogsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditLogsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}
		baseQuery := applyAdminOAuthClientAuditFilters(apiHelper.DBManager.DB.SystemLog.Query(), hydraClient.ClientID, filters)
		total, err := baseQuery.Clone().Count(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditLogsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCountAuditLogsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to count oauth client audit logs")
		}
		offset := (filters.Page - 1) * filters.PageSize
		rows, err := applySystemLogSorting(baseQuery.Clone(), filters.Sort).Limit(filters.PageSize).Offset(offset).All(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditLogsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryAuditLogsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client audit logs")
		}
		resp := adminOAuthClientAuditLogsResponse{GeneratedAt: adminNowUTC(), ClientID: hydraClient.ClientID, ClientName: strings.TrimSpace(hydraClient.ClientName), From: filters.From.UTC(), To: filters.To.UTC(), Page: filters.Page, PageSize: filters.PageSize, Total: total, TotalPages: platformPagination.CalculateTotalPages(total, filters.PageSize), HasMore: platformPagination.HasMoreByOffset(filters.Page, filters.PageSize, total), Sort: filters.Sort, Filters: adminOAuthClientAuditAppliedFilters{ActorTypes: filters.ActorTypes, ActorUserID: filters.ActorUserID, Action: filters.Action, Result: filters.Result}, Items: adminCoreModule.BuildSystemLogItems(rows)}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditLogsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{"hydraMode": true, "from": resp.From.Format(time.RFC3339), "to": resp.To.Format(time.RFC3339), "total": resp.Total})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleGetHydraOAuthClientAuditSummary(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		if clientID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditSummaryQuery, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingClientID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id is required")
		}
		filters, err := parseAdminOAuthClientAuditFilters(c, adminNow())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditSummaryQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidQueryFilters, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid query filters")
		}
		hydraClient, err := oauth2Module.GetHydraOAuthClient(c.Context(), clientID)
		if err != nil {
			if oauth2Module.IsHydraNotFoundError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditSummaryQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditSummaryQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}
		baseQuery := applyAdminOAuthClientAuditFilters(apiHelper.DBManager.DB.SystemLog.Query(), hydraClient.ClientID, filters)
		total, err := baseQuery.Clone().Count(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditSummaryQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCountAuditLogsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to count oauth client audit logs")
		}
		successCount, err := baseQuery.Clone().Where(systemlog.ResultEQ(systemlog.ResultSuccess)).Count(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditSummaryQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCountSuccessAuditLogsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to count successful oauth client audit logs")
		}
		failureCount := total - successCount
		if failureCount < 0 {
			failureCount = 0
		}
		var byActionRows []struct {
			Key   string `json:"action"`
			Count int    `json:"count"`
		}
		if err := baseQuery.Clone().GroupBy(systemlog.FieldAction).Aggregate(postgresql.As(postgresql.Count(), "count")).Scan(c.Context(), &byActionRows); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditSummaryQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonAggregateActionSummaryFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to aggregate oauth client audit action summary")
		}
		byActionCounts := make([]groupedFieldCount, 0, len(byActionRows))
		for _, row := range byActionRows {
			byActionCounts = append(byActionCounts, groupedFieldCount{Key: row.Key, Count: row.Count})
		}
		var byActorTypeRows []struct {
			Key   string `json:"actor_type"`
			Count int    `json:"count"`
		}
		if err := baseQuery.Clone().GroupBy(systemlog.FieldActorType).Aggregate(postgresql.As(postgresql.Count(), "count")).Scan(c.Context(), &byActorTypeRows); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditSummaryQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonAggregateActorTypeSummaryFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to aggregate oauth client audit actor type summary")
		}
		byActorTypeCounts := make([]groupedFieldCount, 0, len(byActorTypeRows))
		for _, row := range byActorTypeRows {
			byActorTypeCounts = append(byActorTypeCounts, groupedFieldCount{Key: row.Key, Count: row.Count})
		}
		var byResultRows []struct {
			Key   string `json:"result"`
			Count int    `json:"count"`
		}
		if err := baseQuery.Clone().GroupBy(systemlog.FieldResult).Aggregate(postgresql.As(postgresql.Count(), "count")).Scan(c.Context(), &byResultRows); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditSummaryQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonAggregateResultSummaryFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to aggregate oauth client audit result summary")
		}
		byResultCounts := make([]groupedFieldCount, 0, len(byResultRows))
		for _, row := range byResultRows {
			byResultCounts = append(byResultCounts, groupedFieldCount{Key: row.Key, Count: row.Count})
		}
		failureReasonRows, err := baseQuery.Clone().Where(systemlog.ResultEQ(systemlog.ResultFailure)).Select(systemlog.FieldMetadata).All(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditSummaryQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonAggregateReasonSummaryFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to aggregate oauth client audit reason summary")
		}
		resp := adminOAuthClientAuditSummaryResponse{GeneratedAt: adminNowUTC(), ClientID: hydraClient.ClientID, ClientName: strings.TrimSpace(hydraClient.ClientName), From: filters.From.UTC(), To: filters.To.UTC(), Total: total, Success: successCount, Failure: failureCount, ByAction: normalizeCategoryCounts(byActionCounts), ByActorType: normalizeCategoryCounts(byActorTypeCounts), ByResult: normalizeCategoryCounts(byResultCounts), ByReason: buildSystemLogFailureReasonCounts(failureReasonRows)}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditSummaryQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{"hydraMode": true, "from": resp.From.Format(time.RFC3339), "to": resp.To.Format(time.RFC3339), "total": resp.Total})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func collectHydraClientAuthorizationRecords(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, clientID string) ([]hydraClientAuthorizationRecord, error) {
	users, err := apiHelper.DBManager.DB.User.Query().
		Select(userSchema.FieldID, userSchema.FieldName, userSchema.FieldEmail, userSchema.FieldRole, userSchema.FieldBanned, userSchema.FieldKratosIdentityID).
		All(ctx)
	if err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return []hydraClientAuthorizationRecord{}, nil
	}

	workerCount := hydraClientAuthorizationScanWorkers
	if len(users) < workerCount {
		workerCount = len(users)
	}

	g, groupCtx := errgroup.WithContext(ctx)
	userCh := make(chan *postgresql.User)
	records := make([]hydraClientAuthorizationRecord, 0)
	var recordsMu sync.Mutex

	for i := 0; i < workerCount; i++ {
		g.Go(func() error {
			localRecords := make([]hydraClientAuthorizationRecord, 0)
			for user := range userCh {
				subjects := oauth2Module.HydraSubjectsForUser(user.ID, user.KratosIdentityID)
				if len(subjects) == 0 {
					continue
				}
				sessions, err := oauth2Module.ListHydraConsentSessionsForSubjects(groupCtx, subjects)
				if err != nil {
					return err
				}
				for _, session := range sessions {
					if strings.TrimSpace(session.ConsentRequest.Client.ClientID) != clientID {
						continue
					}
					localRecords = append(localRecords, hydraClientAuthorizationRecord{
						User:    user,
						Session: session,
						Subject: oauth2Module.PreferredHydraSubject(user.ID, user.KratosIdentityID),
					})
				}
			}
			if len(localRecords) == 0 {
				return nil
			}
			recordsMu.Lock()
			records = append(records, localRecords...)
			recordsMu.Unlock()
			return nil
		})
	}

	for _, user := range users {
		select {
		case <-groupCtx.Done():
			close(userCh)
			if err := g.Wait(); err != nil {
				return nil, err
			}
			return nil, groupCtx.Err()
		case userCh <- user:
		}
	}
	close(userCh)

	if err := g.Wait(); err != nil {
		return nil, err
	}
	return records, nil
}

func hydraClientCreatedAt(client *oauth2Module.HydraOAuthClient) time.Time {
	if client == nil || client.CreatedAt == nil {
		return time.Time{}
	}
	return client.CreatedAt.UTC()
}

func hydraConsentHandledAt(session oauth2Module.HydraConsentSession) time.Time {
	if session.HandledAt != nil {
		return session.HandledAt.UTC()
	}
	return time.Time{}
}

func stableAdminHydraAuthorizationID(consentRequestID, clientID string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.TrimSpace(consentRequestID)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strings.TrimSpace(clientID)))
	return int(h.Sum32() & 0x7fffffff)
}

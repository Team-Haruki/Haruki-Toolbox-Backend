package adminoauth

import (
	"sort"
	"strings"
	"time"

	adminCoreModule "haruki-suite/internal/modules/admincore"
	oauth2Module "haruki-suite/internal/modules/oauth2"
	platformPagination "haruki-suite/internal/platform/pagination"
	harukiAPIHelper "haruki-suite/utils/api"

	"github.com/gofiber/fiber/v3"
)

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

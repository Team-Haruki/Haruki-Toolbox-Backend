package adminoauth

import (
	"strings"
	"time"

	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	oauth2Module "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/oauth2"
	platformPagination "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/pagination"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/systemlog"

	"github.com/gofiber/fiber/v3"
)

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

package adminoauth

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	platformPagination "haruki-suite/internal/platform/pagination"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/oauthclient"
	"haruki-suite/utils/database/postgresql/systemlog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

type adminOAuthClientAuditFilters struct {
	From        time.Time
	To          time.Time
	ActorTypes  []string
	ActorUserID string
	Action      string
	Result      string
	Page        int
	PageSize    int
	Sort        string
}

type adminOAuthClientAuditAppliedFilters struct {
	ActorTypes  []string `json:"actorTypes,omitempty"`
	ActorUserID string   `json:"actorUserId,omitempty"`
	Action      string   `json:"action,omitempty"`
	Result      string   `json:"result,omitempty"`
}

type adminOAuthClientAuditLogsResponse struct {
	GeneratedAt time.Time                           `json:"generatedAt"`
	ClientID    string                              `json:"clientId"`
	ClientName  string                              `json:"clientName"`
	From        time.Time                           `json:"from"`
	To          time.Time                           `json:"to"`
	Page        int                                 `json:"page"`
	PageSize    int                                 `json:"pageSize"`
	Total       int                                 `json:"total"`
	TotalPages  int                                 `json:"totalPages"`
	HasMore     bool                                `json:"hasMore"`
	Sort        string                              `json:"sort"`
	Filters     adminOAuthClientAuditAppliedFilters `json:"filters"`
	Items       []systemLogListItem                 `json:"items"`
}

type adminOAuthClientAuditSummaryResponse struct {
	GeneratedAt time.Time       `json:"generatedAt"`
	ClientID    string          `json:"clientId"`
	ClientName  string          `json:"clientName"`
	From        time.Time       `json:"from"`
	To          time.Time       `json:"to"`
	Total       int             `json:"total"`
	Success     int             `json:"success"`
	Failure     int             `json:"failure"`
	ByAction    []categoryCount `json:"byAction"`
	ByActorType []categoryCount `json:"byActorType"`
	ByResult    []categoryCount `json:"byResult"`
	ByReason    []categoryCount `json:"byReason"`
}

func parseAdminOAuthClientAuditFilters(c fiber.Ctx, now time.Time) (*adminOAuthClientAuditFilters, error) {
	from, to, err := resolveUploadLogTimeRange(c.Query("from"), c.Query("to"), now)
	if err != nil {
		return nil, err
	}

	actorTypes, err := parseSystemLogActorTypesFilter(c.Query("actor_type"))
	if err != nil {
		return nil, err
	}
	result, err := parseSystemLogResultFilter(c.Query("result"))
	if err != nil {
		return nil, err
	}
	page, pageSize, err := platformPagination.ParsePageAndPageSize(c, defaultSystemLogPage, defaultSystemLogPageSize, maxSystemLogPageSize)
	if err != nil {
		return nil, err
	}
	sortValue, err := parseSystemLogSort(c.Query("sort"))
	if err != nil {
		return nil, err
	}

	return &adminOAuthClientAuditFilters{
		From:        from,
		To:          to,
		ActorTypes:  actorTypes,
		ActorUserID: strings.TrimSpace(c.Query("actor_user_id")),
		Action:      strings.TrimSpace(c.Query("action")),
		Result:      result,
		Page:        page,
		PageSize:    pageSize,
		Sort:        sortValue,
	}, nil
}

func applyAdminOAuthClientAuditFilters(query *postgresql.SystemLogQuery, clientID string, filters *adminOAuthClientAuditFilters) *postgresql.SystemLogQuery {
	q := query.Where(
		systemlog.EventTimeGTE(filters.From),
		systemlog.EventTimeLTE(filters.To),
		systemlog.TargetTypeEQ(adminAuditTargetTypeOAuthClient),
		systemlog.TargetIDEQ(clientID),
	)

	if len(filters.ActorTypes) > 0 {
		types := make([]systemlog.ActorType, 0, len(filters.ActorTypes))
		for _, actorType := range filters.ActorTypes {
			types = append(types, systemlog.ActorType(actorType))
		}
		q = q.Where(systemlog.ActorTypeIn(types...))
	}
	if filters.ActorUserID != "" {
		q = q.Where(systemlog.ActorUserIDEQ(filters.ActorUserID))
	}
	if filters.Action != "" {
		q = q.Where(systemlog.ActionContainsFold(filters.Action))
	}
	if filters.Result != "" {
		q = q.Where(systemlog.ResultEQ(systemlog.Result(filters.Result)))
	}

	return q
}

func handleListOAuthClientAuditLogs(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
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

		dbClient, err := apiHelper.DBManager.DB.OAuthClient.Query().
			Where(oauthclient.ClientIDEQ(clientID)).
			Select(oauthclient.FieldClientID, oauthclient.FieldName).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditLogsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditLogsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}

		baseQuery := applyAdminOAuthClientAuditFilters(apiHelper.DBManager.DB.SystemLog.Query(), dbClient.ClientID, filters)
		total, err := baseQuery.Clone().Count(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditLogsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCountAuditLogsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to count oauth client audit logs")
		}

		offset := (filters.Page - 1) * filters.PageSize
		rows, err := applySystemLogSorting(baseQuery.Clone(), filters.Sort).
			Limit(filters.PageSize).
			Offset(offset).
			All(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditLogsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryAuditLogsFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client audit logs")
		}

		totalPages := platformPagination.CalculateTotalPages(total, filters.PageSize)

		resp := adminOAuthClientAuditLogsResponse{
			GeneratedAt: adminNowUTC(),
			ClientID:    dbClient.ClientID,
			ClientName:  dbClient.Name,
			From:        filters.From.UTC(),
			To:          filters.To.UTC(),
			Page:        filters.Page,
			PageSize:    filters.PageSize,
			Total:       total,
			TotalPages:  totalPages,
			HasMore:     platformPagination.HasMoreByOffset(filters.Page, filters.PageSize, total),
			Sort:        filters.Sort,
			Filters: adminOAuthClientAuditAppliedFilters{
				ActorTypes:  filters.ActorTypes,
				ActorUserID: filters.ActorUserID,
				Action:      filters.Action,
				Result:      filters.Result,
			},
			Items: adminCoreModule.BuildSystemLogItems(rows),
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditLogsQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"from":  resp.From.Format(time.RFC3339),
			"to":    resp.To.Format(time.RFC3339),
			"total": resp.Total,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleGetOAuthClientAuditSummary(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
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

		dbClient, err := apiHelper.DBManager.DB.OAuthClient.Query().
			Where(oauthclient.ClientIDEQ(clientID)).
			Select(oauthclient.FieldClientID, oauthclient.FieldName).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditSummaryQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditSummaryQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}

		baseQuery := applyAdminOAuthClientAuditFilters(apiHelper.DBManager.DB.SystemLog.Query(), dbClient.ClientID, filters)
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
		if err := baseQuery.Clone().
			GroupBy(systemlog.FieldAction).
			Aggregate(postgresql.As(postgresql.Count(), "count")).
			Scan(c.Context(), &byActionRows); err != nil {
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
		if err := baseQuery.Clone().
			GroupBy(systemlog.FieldActorType).
			Aggregate(postgresql.As(postgresql.Count(), "count")).
			Scan(c.Context(), &byActorTypeRows); err != nil {
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
		if err := baseQuery.Clone().
			GroupBy(systemlog.FieldResult).
			Aggregate(postgresql.As(postgresql.Count(), "count")).
			Scan(c.Context(), &byResultRows); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditSummaryQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonAggregateResultSummaryFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to aggregate oauth client audit result summary")
		}
		byResultCounts := make([]groupedFieldCount, 0, len(byResultRows))
		for _, row := range byResultRows {
			byResultCounts = append(byResultCounts, groupedFieldCount{Key: row.Key, Count: row.Count})
		}

		failureReasonRows, err := baseQuery.Clone().
			Where(systemlog.ResultEQ(systemlog.ResultFailure)).
			Select(systemlog.FieldMetadata).
			All(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditSummaryQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonAggregateReasonSummaryFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to aggregate oauth client audit reason summary")
		}

		resp := adminOAuthClientAuditSummaryResponse{
			GeneratedAt: adminNowUTC(),
			ClientID:    dbClient.ClientID,
			ClientName:  dbClient.Name,
			From:        filters.From.UTC(),
			To:          filters.To.UTC(),
			Total:       total,
			Success:     successCount,
			Failure:     failureCount,
			ByAction:    normalizeCategoryCounts(byActionCounts),
			ByActorType: normalizeCategoryCounts(byActorTypeCounts),
			ByResult:    normalizeCategoryCounts(byResultCounts),
			ByReason:    buildSystemLogFailureReasonCounts(failureReasonRows),
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientAuditSummaryQuery, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"from":    resp.From.Format(time.RFC3339),
			"to":      resp.To.Format(time.RFC3339),
			"total":   resp.Total,
			"success": resp.Success,
			"failure": resp.Failure,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

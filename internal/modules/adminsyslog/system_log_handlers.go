package adminsyslog

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	platformPagination "haruki-suite/internal/platform/pagination"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/systemlog"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func handleQuerySystemLogs(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		filters, err := parseSystemLogQueryFilters(c, adminNow())
		if err != nil {
			return respondFiberOrBadRequest(c, err, "invalid query filters")
		}

		dbCtx := c.Context()
		baseQuery := applySystemLogFilters(apiHelper.DBManager.DB.SystemLog.Query(), filters)

		total, err := baseQuery.Clone().Count(dbCtx)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to count system logs")
		}

		offset := (filters.Page - 1) * filters.PageSize
		rows, err := applySystemLogSorting(baseQuery.Clone(), filters.Sort).
			Limit(filters.PageSize).
			Offset(offset).
			All(dbCtx)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query system logs")
		}

		totalPages := platformPagination.CalculateTotalPages(total, filters.PageSize)

		resp := systemLogQueryResponse{
			GeneratedAt: adminNowUTC(),
			From:        filters.From.UTC(),
			To:          filters.To.UTC(),
			Page:        filters.Page,
			PageSize:    filters.PageSize,
			Total:       total,
			TotalPages:  totalPages,
			HasMore:     platformPagination.HasMoreByOffset(filters.Page, filters.PageSize, total),
			Sort:        filters.Sort,
			Filters: systemLogAppliedFilters{
				ActorTypes:  filters.ActorTypes,
				ActorUserID: filters.ActorUserID,
				TargetType:  filters.TargetType,
				TargetID:    filters.TargetID,
				Action:      filters.Action,
				Result:      filters.Result,
			},
			Items: adminCoreModule.BuildSystemLogItems(rows),
		}

		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleGetSystemLogSummary(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		filters, err := parseSystemLogQueryFilters(c, adminNow())
		if err != nil {
			return respondFiberOrBadRequest(c, err, "invalid query filters")
		}

		dbCtx := c.Context()
		baseQuery := applySystemLogFilters(apiHelper.DBManager.DB.SystemLog.Query(), filters)

		total, err := baseQuery.Clone().Count(dbCtx)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to count system logs")
		}
		successCount, err := baseQuery.Clone().Where(systemlog.ResultEQ(systemlog.ResultSuccess)).Count(dbCtx)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to count successful system logs")
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
			Scan(dbCtx, &byActionRows); err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to aggregate system log action summary")
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
			Scan(dbCtx, &byActorTypeRows); err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to aggregate system log actor type summary")
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
			Scan(dbCtx, &byResultRows); err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to aggregate system log result summary")
		}
		byResultCounts := make([]groupedFieldCount, 0, len(byResultRows))
		for _, row := range byResultRows {
			byResultCounts = append(byResultCounts, groupedFieldCount{Key: row.Key, Count: row.Count})
		}

		failureReasonRows, err := baseQuery.Clone().
			Where(systemlog.ResultEQ(systemlog.ResultFailure)).
			Select(systemlog.FieldMetadata).
			All(dbCtx)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to aggregate system log reason summary")
		}

		resp := systemLogSummaryResponse{
			GeneratedAt: adminNowUTC(),
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
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleGetSystemLogDetail(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		idValue := strings.TrimSpace(c.Params("id"))
		if idValue == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "id is required")
		}
		id, err := strconv.Atoi(idValue)
		if err != nil || id <= 0 {
			return harukiAPIHelper.ErrorBadRequest(c, "id must be a positive integer")
		}

		row, err := apiHelper.DBManager.DB.SystemLog.Query().
			Where(systemlog.IDEQ(id)).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				return harukiAPIHelper.ErrorNotFound(c, "system log not found")
			}
			return harukiAPIHelper.ErrorInternal(c, "failed to query system log detail")
		}

		items := adminCoreModule.BuildSystemLogItems([]*postgresql.SystemLog{row})
		if len(items) == 0 {
			return harukiAPIHelper.ErrorNotFound(c, "system log not found")
		}
		return harukiAPIHelper.SuccessResponse(c, "success", &items[0])
	}
}

func handleExportSystemLogs(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		filters, err := parseSystemLogQueryFilters(c, adminNow())
		if err != nil {
			return respondFiberOrBadRequest(c, err, "invalid query filters")
		}

		limit, err := parseSystemLogExportLimit(c.Query("limit"))
		if err != nil {
			return respondFiberOrBadRequest(c, err, "invalid export limit")
		}

		format := strings.ToLower(strings.TrimSpace(c.Query("format")))
		if format == "" {
			format = "json"
		}
		if format != "json" && format != "csv" {
			return harukiAPIHelper.ErrorBadRequest(c, "format must be one of: json, csv")
		}

		baseQuery := applySystemLogFilters(apiHelper.DBManager.DB.SystemLog.Query(), filters)
		rows, err := applySystemLogSorting(baseQuery.Clone(), filters.Sort).
			Limit(limit).
			All(c.Context())
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to export system logs")
		}

		items := adminCoreModule.BuildSystemLogItems(rows)
		if format == "csv" {
			data, err := buildSystemLogCSV(items)
			if err != nil {
				return harukiAPIHelper.ErrorInternal(c, "failed to build csv export")
			}
			fileName := "system_logs_" + adminNowUTC().Format("20060102_150405") + ".csv"
			c.Set(fiber.HeaderContentType, "text/csv; charset=utf-8")
			c.Set(fiber.HeaderContentDisposition, `attachment; filename="`+fileName+`"`)
			return c.Status(fiber.StatusOK).Send(data)
		}

		resp := systemLogQueryResponse{
			GeneratedAt: adminNowUTC(),
			From:        filters.From.UTC(),
			To:          filters.To.UTC(),
			Page:        1,
			PageSize:    limit,
			Total:       len(items),
			TotalPages:  1,
			HasMore:     false,
			Sort:        filters.Sort,
			Filters: systemLogAppliedFilters{
				ActorTypes:  filters.ActorTypes,
				ActorUserID: filters.ActorUserID,
				TargetType:  filters.TargetType,
				TargetID:    filters.TargetID,
				Action:      filters.Action,
				Result:      filters.Result,
			},
			Items: items,
		}
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

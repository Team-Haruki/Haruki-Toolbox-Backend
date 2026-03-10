package adminstats

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	platformPagination "haruki-suite/internal/platform/pagination"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/uploadlog"

	"github.com/gofiber/fiber/v3"
)

func handleQueryUploadLogs(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		filters, err := parseUploadLogQueryFilters(c, adminNow())
		if err != nil {
			return respondFiberOrBadRequest(c, err, "invalid query filters")
		}

		dbCtx := c.Context()
		baseQuery := applyUploadLogFilters(apiHelper.DBManager.DB.UploadLog.Query(), filters)

		total, err := baseQuery.Clone().Count(dbCtx)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to count upload logs")
		}

		successCount, err := baseQuery.Clone().Where(uploadlog.SuccessEQ(true)).Count(dbCtx)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to count successful upload logs")
		}
		failedCount := total - successCount
		if failedCount < 0 {
			failedCount = 0
		}

		var byMethodRows []struct {
			Key   string `json:"upload_method"`
			Count int    `json:"count"`
		}
		if err := baseQuery.Clone().
			GroupBy(uploadlog.FieldUploadMethod).
			Aggregate(postgresql.As(postgresql.Count(), "count")).
			Scan(dbCtx, &byMethodRows); err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to aggregate upload method statistics")
		}
		methodCounts := make([]groupedFieldCount, 0, len(byMethodRows))
		for _, row := range byMethodRows {
			methodCounts = append(methodCounts, groupedFieldCount{Key: row.Key, Count: row.Count})
		}

		var byDataTypeRows []struct {
			Key   string `json:"data_type"`
			Count int    `json:"count"`
		}
		if err := baseQuery.Clone().
			GroupBy(uploadlog.FieldDataType).
			Aggregate(postgresql.As(postgresql.Count(), "count")).
			Scan(dbCtx, &byDataTypeRows); err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to aggregate data type statistics")
		}
		dataTypeCounts := make([]groupedFieldCount, 0, len(byDataTypeRows))
		for _, row := range byDataTypeRows {
			dataTypeCounts = append(dataTypeCounts, groupedFieldCount{Key: row.Key, Count: row.Count})
		}

		offset := (filters.Page - 1) * filters.PageSize
		rows, err := applyUploadLogSorting(baseQuery.Clone(), filters.Sort).
			Limit(filters.PageSize).
			Offset(offset).
			All(dbCtx)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query upload logs")
		}

		totalPages := platformPagination.CalculateTotalPages(total, filters.PageSize)

		resp := uploadLogQueryResponse{
			GeneratedAt: adminNowUTC(),
			From:        filters.From.UTC(),
			To:          filters.To.UTC(),
			Page:        filters.Page,
			PageSize:    filters.PageSize,
			Total:       total,
			TotalPages:  totalPages,
			HasMore:     platformPagination.HasMoreByOffset(filters.Page, filters.PageSize, total),
			Sort:        filters.Sort,
			Filters: uploadLogAppliedFilters{
				GameUserIDs:   filters.GameUserIDs,
				UploadMethods: filters.UploadMethods,
				DataTypes:     filters.DataTypes,
				Servers:       filters.Servers,
				Success:       filters.Success,
			},
			Summary: uploadLogQuerySummary{
				Success:    successCount,
				Failed:     failedCount,
				ByMethod:   normalizeCategoryCounts(methodCounts),
				ByDataType: normalizeCategoryCounts(dataTypeCounts),
			},
			Items: adminCoreModule.BuildUploadLogItems(rows),
		}

		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

package admin

import (
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/uploadlog"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

const (
	defaultUploadLogPage        = 1
	defaultUploadLogPageSize    = 50
	maxUploadLogPageSize        = 200
	defaultUploadLogWindowHours = 24
	maxUploadLogTimeRangeHours  = 24 * 30
	defaultUploadLogSort        = "upload_time_desc"
	uploadLogSortUploadTimeDesc = "upload_time_desc"
	uploadLogSortUploadTimeAsc  = "upload_time_asc"
	uploadLogSortIDDesc         = "id_desc"
	uploadLogSortIDAsc          = "id_asc"
)

var validUploadMethods = []string{
	string(harukiUtils.UploadMethodManual),
	string(harukiUtils.UploadMethodIOSProxy),
	string(harukiUtils.UploadMethodIOSScript),
	string(harukiUtils.UploadMethodHarukiProxy),
	string(harukiUtils.UploadMethodInherit),
}

type uploadLogQueryFilters struct {
	From          time.Time
	To            time.Time
	GameUserIDs   []string
	UploadMethods []string
	DataTypes     []string
	Servers       []string
	Success       *bool
	Page          int
	PageSize      int
	Sort          string
}

type uploadLogListItem struct {
	ID            int       `json:"id"`
	Server        string    `json:"server"`
	GameUserID    string    `json:"gameUserId"`
	ToolboxUserID string    `json:"toolboxUserId,omitempty"`
	DataType      string    `json:"dataType"`
	UploadMethod  string    `json:"uploadMethod"`
	Success       bool      `json:"success"`
	UploadTime    time.Time `json:"uploadTime"`
}

type uploadLogAppliedFilters struct {
	GameUserIDs   []string `json:"gameUserIds,omitempty"`
	UploadMethods []string `json:"uploadMethods,omitempty"`
	DataTypes     []string `json:"dataTypes,omitempty"`
	Servers       []string `json:"servers,omitempty"`
	Success       *bool    `json:"success,omitempty"`
}

type uploadLogQuerySummary struct {
	Success    int             `json:"success"`
	Failed     int             `json:"failed"`
	ByMethod   []categoryCount `json:"byMethod"`
	ByDataType []categoryCount `json:"byDataType"`
}

type uploadLogQueryResponse struct {
	GeneratedAt time.Time               `json:"generatedAt"`
	From        time.Time               `json:"from"`
	To          time.Time               `json:"to"`
	Page        int                     `json:"page"`
	PageSize    int                     `json:"pageSize"`
	Total       int                     `json:"total"`
	TotalPages  int                     `json:"totalPages"`
	HasMore     bool                    `json:"hasMore"`
	Sort        string                  `json:"sort"`
	Filters     uploadLogAppliedFilters `json:"filters"`
	Summary     uploadLogQuerySummary   `json:"summary"`
	Items       []uploadLogListItem     `json:"items"`
}

func parseCSVValues(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}

	parts := strings.Split(trimmed, ",")
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}

	return result
}

func parseUploadMethodsFilter(raw string) ([]string, error) {
	values := parseCSVValues(raw)
	if len(values) == 0 {
		return nil, nil
	}

	for _, value := range values {
		if !slices.Contains(validUploadMethods, value) {
			return nil, fiber.NewError(fiber.StatusBadRequest, "invalid upload_method filter")
		}
	}
	return values, nil
}

func parseGameUserIDsFilter(raw string) ([]string, error) {
	values := parseCSVValues(raw)
	if len(values) == 0 {
		return nil, nil
	}
	return values, nil
}

func parseDataTypesFilter(raw string) ([]string, error) {
	values := parseCSVValues(raw)
	if len(values) == 0 {
		return nil, nil
	}

	for _, value := range values {
		if _, err := harukiUtils.ParseUploadDataType(value); err != nil {
			return nil, fiber.NewError(fiber.StatusBadRequest, "invalid data_type filter")
		}
	}
	return values, nil
}

func parseServersFilter(raw string) ([]string, error) {
	values := parseCSVValues(raw)
	if len(values) == 0 {
		return nil, nil
	}

	for _, value := range values {
		if _, err := harukiUtils.ParseSupportedDataUploadServer(value); err != nil {
			return nil, fiber.NewError(fiber.StatusBadRequest, "invalid server filter")
		}
	}
	return values, nil
}

func parseOptionalBool(raw string) (*bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	v, err := strconv.ParseBool(trimmed)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid success filter")
	}
	return &v, nil
}

func parsePositiveInt(raw string, defaultValue int, fieldName string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultValue, nil
	}
	v, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fiber.NewError(fiber.StatusBadRequest, fieldName+" must be an integer")
	}
	if v <= 0 {
		return 0, fiber.NewError(fiber.StatusBadRequest, fieldName+" must be greater than 0")
	}
	return v, nil
}

func parseUploadLogSort(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultUploadLogSort, nil
	}

	switch trimmed {
	case uploadLogSortUploadTimeDesc, uploadLogSortUploadTimeAsc, uploadLogSortIDDesc, uploadLogSortIDAsc:
		return trimmed, nil
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid sort option")
	}
}

func parseFlexibleTime(raw string) (*time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}

	if unixVal, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		var t time.Time
		if len(trimmed) > 10 {
			t = time.UnixMilli(unixVal).UTC()
		} else {
			t = time.Unix(unixVal, 0).UTC()
		}
		return &t, nil
	}

	t, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid time format, use RFC3339 or unix timestamp")
	}
	t = t.UTC()
	return &t, nil
}

func resolveUploadLogTimeRange(fromRaw, toRaw string, now time.Time) (time.Time, time.Time, error) {
	fromValue, err := parseFlexibleTime(fromRaw)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	toValue, err := parseFlexibleTime(toRaw)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	now = now.UTC()
	var from time.Time
	var to time.Time

	switch {
	case fromValue == nil && toValue == nil:
		to = now
		from = to.Add(-defaultUploadLogWindowHours * time.Hour)
	case fromValue == nil && toValue != nil:
		to = *toValue
		from = to.Add(-defaultUploadLogWindowHours * time.Hour)
	case fromValue != nil && toValue == nil:
		from = *fromValue
		to = now
	default:
		from = *fromValue
		to = *toValue
	}

	if !from.Before(to) {
		return time.Time{}, time.Time{}, fiber.NewError(fiber.StatusBadRequest, "from must be earlier than to")
	}
	if to.Sub(from) > maxUploadLogTimeRangeHours*time.Hour {
		return time.Time{}, time.Time{}, fiber.NewError(fiber.StatusBadRequest, "time range exceeds max allowed window")
	}

	return from, to, nil
}

func parseUploadLogQueryFilters(c fiber.Ctx, now time.Time) (*uploadLogQueryFilters, error) {
	from, to, err := resolveUploadLogTimeRange(c.Query("from"), c.Query("to"), now)
	if err != nil {
		return nil, err
	}

	gameUserIDs, err := parseGameUserIDsFilter(c.Query("game_user_id"))
	if err != nil {
		return nil, err
	}
	uploadMethods, err := parseUploadMethodsFilter(c.Query("upload_method"))
	if err != nil {
		return nil, err
	}
	dataTypes, err := parseDataTypesFilter(c.Query("data_type"))
	if err != nil {
		return nil, err
	}
	servers, err := parseServersFilter(c.Query("server"))
	if err != nil {
		return nil, err
	}
	success, err := parseOptionalBool(c.Query("success"))
	if err != nil {
		return nil, err
	}

	page, err := parsePositiveInt(c.Query("page"), defaultUploadLogPage, "page")
	if err != nil {
		return nil, err
	}
	pageSize, err := parsePositiveInt(c.Query("page_size"), defaultUploadLogPageSize, "page_size")
	if err != nil {
		return nil, err
	}
	if pageSize > maxUploadLogPageSize {
		return nil, fiber.NewError(fiber.StatusBadRequest, "page_size exceeds max allowed size")
	}

	sortValue, err := parseUploadLogSort(c.Query("sort"))
	if err != nil {
		return nil, err
	}

	return &uploadLogQueryFilters{
		From:          from,
		To:            to,
		GameUserIDs:   gameUserIDs,
		UploadMethods: uploadMethods,
		DataTypes:     dataTypes,
		Servers:       servers,
		Success:       success,
		Page:          page,
		PageSize:      pageSize,
		Sort:          sortValue,
	}, nil
}

func applyUploadLogFilters(query *postgresql.UploadLogQuery, filters *uploadLogQueryFilters) *postgresql.UploadLogQuery {
	q := query.Where(
		uploadlog.UploadTimeGTE(filters.From),
		uploadlog.UploadTimeLTE(filters.To),
	)
	if len(filters.GameUserIDs) > 0 {
		q = q.Where(uploadlog.GameUserIDIn(filters.GameUserIDs...))
	}
	if len(filters.UploadMethods) > 0 {
		q = q.Where(uploadlog.UploadMethodIn(filters.UploadMethods...))
	}
	if len(filters.DataTypes) > 0 {
		q = q.Where(uploadlog.DataTypeIn(filters.DataTypes...))
	}
	if len(filters.Servers) > 0 {
		q = q.Where(uploadlog.ServerIn(filters.Servers...))
	}
	if filters.Success != nil {
		q = q.Where(uploadlog.SuccessEQ(*filters.Success))
	}
	return q
}

func applyUploadLogSorting(query *postgresql.UploadLogQuery, sortValue string) *postgresql.UploadLogQuery {
	switch sortValue {
	case uploadLogSortUploadTimeAsc:
		return query.Order(uploadlog.ByUploadTime(sql.OrderAsc()), uploadlog.ByID(sql.OrderAsc()))
	case uploadLogSortIDDesc:
		return query.Order(uploadlog.ByID(sql.OrderDesc()))
	case uploadLogSortIDAsc:
		return query.Order(uploadlog.ByID(sql.OrderAsc()))
	default:
		return query.Order(uploadlog.ByUploadTime(sql.OrderDesc()), uploadlog.ByID(sql.OrderDesc()))
	}
}

func buildUploadLogItems(rows []*postgresql.UploadLog) []uploadLogListItem {
	items := make([]uploadLogListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, uploadLogListItem{
			ID:            row.ID,
			Server:        row.Server,
			GameUserID:    row.GameUserID,
			ToolboxUserID: row.ToolboxUserID,
			DataType:      row.DataType,
			UploadMethod:  row.UploadMethod,
			Success:       row.Success,
			UploadTime:    row.UploadTime.UTC(),
		})
	}
	return items
}

func handleQueryUploadLogs(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		filters, err := parseUploadLogQueryFilters(c, time.Now())
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid query filters")
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

		totalPages := 0
		if total > 0 {
			totalPages = int(math.Ceil(float64(total) / float64(filters.PageSize)))
		}

		resp := uploadLogQueryResponse{
			GeneratedAt: time.Now().UTC(),
			From:        filters.From.UTC(),
			To:          filters.To.UTC(),
			Page:        filters.Page,
			PageSize:    filters.PageSize,
			Total:       total,
			TotalPages:  totalPages,
			HasMore:     filters.Page*filters.PageSize < total,
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
			Items: buildUploadLogItems(rows),
		}

		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

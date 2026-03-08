package admin

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/systemlog"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

const (
	defaultSystemLogPage       = 1
	defaultSystemLogPageSize   = 50
	maxSystemLogPageSize       = 200
	defaultSystemLogExportSize = 1000
	maxSystemLogExportSize     = 5000
	defaultSystemLogSort       = "event_time_desc"
	systemLogSortEventTimeAsc  = "event_time_asc"
	systemLogSortEventTimeDesc = "event_time_desc"
	systemLogSortIDAsc         = "id_asc"
	systemLogSortIDDesc        = "id_desc"
)

var validSystemLogActorTypes = []string{
	harukiAPIHelper.SystemLogActorTypeAnonymous,
	harukiAPIHelper.SystemLogActorTypeUser,
	harukiAPIHelper.SystemLogActorTypeAdmin,
	harukiAPIHelper.SystemLogActorTypeSystem,
}

var validSystemLogResults = []string{
	harukiAPIHelper.SystemLogResultSuccess,
	harukiAPIHelper.SystemLogResultFailure,
}

type systemLogQueryFilters struct {
	From        time.Time
	To          time.Time
	ActorTypes  []string
	ActorUserID string
	TargetType  string
	TargetID    string
	Action      string
	Result      string
	Page        int
	PageSize    int
	Sort        string
}

type systemLogListItem struct {
	ID          int            `json:"id"`
	EventTime   time.Time      `json:"eventTime"`
	ActorUserID string         `json:"actorUserId,omitempty"`
	ActorRole   string         `json:"actorRole,omitempty"`
	ActorType   string         `json:"actorType"`
	Action      string         `json:"action"`
	TargetType  string         `json:"targetType,omitempty"`
	TargetID    string         `json:"targetId,omitempty"`
	Result      string         `json:"result"`
	IP          string         `json:"ip,omitempty"`
	UserAgent   string         `json:"userAgent,omitempty"`
	Method      string         `json:"method,omitempty"`
	Path        string         `json:"path,omitempty"`
	RequestID   string         `json:"requestId,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type systemLogAppliedFilters struct {
	ActorTypes  []string `json:"actorTypes,omitempty"`
	ActorUserID string   `json:"actorUserId,omitempty"`
	TargetType  string   `json:"targetType,omitempty"`
	TargetID    string   `json:"targetId,omitempty"`
	Action      string   `json:"action,omitempty"`
	Result      string   `json:"result,omitempty"`
}

type systemLogQueryResponse struct {
	GeneratedAt time.Time               `json:"generatedAt"`
	From        time.Time               `json:"from"`
	To          time.Time               `json:"to"`
	Page        int                     `json:"page"`
	PageSize    int                     `json:"pageSize"`
	Total       int                     `json:"total"`
	TotalPages  int                     `json:"totalPages"`
	HasMore     bool                    `json:"hasMore"`
	Sort        string                  `json:"sort"`
	Filters     systemLogAppliedFilters `json:"filters"`
	Items       []systemLogListItem     `json:"items"`
}

type systemLogSummaryResponse struct {
	GeneratedAt time.Time       `json:"generatedAt"`
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

const unknownSystemLogFailureReason = "unknown"

func normalizeSystemLogFailureReason(metadata map[string]any) string {
	if metadata == nil {
		return unknownSystemLogFailureReason
	}

	rawReason, ok := metadata["reason"]
	if !ok {
		return unknownSystemLogFailureReason
	}

	reason, ok := rawReason.(string)
	if !ok {
		return unknownSystemLogFailureReason
	}

	reason = strings.TrimSpace(reason)
	if reason == "" {
		return unknownSystemLogFailureReason
	}
	return reason
}

func buildSystemLogFailureReasonCounts(rows []*postgresql.SystemLog) []categoryCount {
	counter := make(map[string]int, len(rows))
	for _, row := range rows {
		reason := normalizeSystemLogFailureReason(row.Metadata)
		counter[reason]++
	}

	grouped := make([]groupedFieldCount, 0, len(counter))
	for reason, count := range counter {
		grouped = append(grouped, groupedFieldCount{Key: reason, Count: count})
	}
	return normalizeCategoryCounts(grouped)
}

func parseSystemLogActorTypesFilter(raw string) ([]string, error) {
	values := parseCSVValues(raw)
	if len(values) == 0 {
		return nil, nil
	}

	for _, v := range values {
		if !slices.Contains(validSystemLogActorTypes, strings.ToLower(v)) {
			return nil, fiber.NewError(fiber.StatusBadRequest, "invalid actor_type filter")
		}
	}

	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, strings.ToLower(v))
	}
	return out, nil
}

func parseSystemLogResultFilter(raw string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return "", nil
	}
	if !slices.Contains(validSystemLogResults, trimmed) {
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid result filter")
	}
	return trimmed, nil
}

func parseSystemLogSort(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultSystemLogSort, nil
	}

	switch trimmed {
	case systemLogSortEventTimeDesc, systemLogSortEventTimeAsc, systemLogSortIDDesc, systemLogSortIDAsc:
		return trimmed, nil
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid sort option")
	}
}

func parseSystemLogQueryFilters(c fiber.Ctx, now time.Time) (*systemLogQueryFilters, error) {
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
	page, err := parsePositiveInt(c.Query("page"), defaultSystemLogPage, "page")
	if err != nil {
		return nil, err
	}
	pageSize, err := parsePositiveInt(c.Query("page_size"), defaultSystemLogPageSize, "page_size")
	if err != nil {
		return nil, err
	}
	if pageSize > maxSystemLogPageSize {
		return nil, fiber.NewError(fiber.StatusBadRequest, "page_size exceeds max allowed size")
	}
	sortValue, err := parseSystemLogSort(c.Query("sort"))
	if err != nil {
		return nil, err
	}

	return &systemLogQueryFilters{
		From:        from,
		To:          to,
		ActorTypes:  actorTypes,
		ActorUserID: strings.TrimSpace(c.Query("actor_user_id")),
		TargetType:  strings.TrimSpace(c.Query("target_type")),
		TargetID:    strings.TrimSpace(c.Query("target_id")),
		Action:      strings.TrimSpace(c.Query("action")),
		Result:      result,
		Page:        page,
		PageSize:    pageSize,
		Sort:        sortValue,
	}, nil
}

func applySystemLogFilters(query *postgresql.SystemLogQuery, filters *systemLogQueryFilters) *postgresql.SystemLogQuery {
	q := query.Where(
		systemlog.EventTimeGTE(filters.From),
		systemlog.EventTimeLTE(filters.To),
	)
	if len(filters.ActorTypes) > 0 {
		types := make([]systemlog.ActorType, 0, len(filters.ActorTypes))
		for _, t := range filters.ActorTypes {
			types = append(types, systemlog.ActorType(t))
		}
		q = q.Where(systemlog.ActorTypeIn(types...))
	}
	if filters.ActorUserID != "" {
		q = q.Where(systemlog.ActorUserIDEQ(filters.ActorUserID))
	}
	if filters.TargetType != "" {
		q = q.Where(systemlog.TargetTypeEQ(filters.TargetType))
	}
	if filters.TargetID != "" {
		q = q.Where(systemlog.TargetIDContainsFold(filters.TargetID))
	}
	if filters.Action != "" {
		q = q.Where(systemlog.ActionContainsFold(filters.Action))
	}
	if filters.Result != "" {
		q = q.Where(systemlog.ResultEQ(systemlog.Result(filters.Result)))
	}
	return q
}

func applySystemLogSorting(query *postgresql.SystemLogQuery, sortValue string) *postgresql.SystemLogQuery {
	switch sortValue {
	case systemLogSortEventTimeAsc:
		return query.Order(systemlog.ByEventTime(sql.OrderAsc()), systemlog.ByID(sql.OrderAsc()))
	case systemLogSortIDDesc:
		return query.Order(systemlog.ByID(sql.OrderDesc()))
	case systemLogSortIDAsc:
		return query.Order(systemlog.ByID(sql.OrderAsc()))
	default:
		return query.Order(systemlog.ByEventTime(sql.OrderDesc()), systemlog.ByID(sql.OrderDesc()))
	}
}

func buildSystemLogItems(rows []*postgresql.SystemLog) []systemLogListItem {
	items := make([]systemLogListItem, 0, len(rows))
	for _, row := range rows {
		item := systemLogListItem{
			ID:        row.ID,
			EventTime: row.EventTime.UTC(),
			ActorType: string(row.ActorType),
			Action:    row.Action,
			Result:    string(row.Result),
			Metadata:  row.Metadata,
		}
		if row.ActorUserID != nil {
			item.ActorUserID = *row.ActorUserID
		}
		if row.ActorRole != nil {
			item.ActorRole = *row.ActorRole
		}
		if row.TargetType != nil {
			item.TargetType = *row.TargetType
		}
		if row.TargetID != nil {
			item.TargetID = *row.TargetID
		}
		if row.IP != nil {
			item.IP = *row.IP
		}
		if row.UserAgent != nil {
			item.UserAgent = *row.UserAgent
		}
		if row.Method != nil {
			item.Method = *row.Method
		}
		if row.Path != nil {
			item.Path = *row.Path
		}
		if row.RequestID != nil {
			item.RequestID = *row.RequestID
		}
		items = append(items, item)
	}
	return items
}

func handleQuerySystemLogs(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		filters, err := parseSystemLogQueryFilters(c, time.Now())
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid query filters")
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

		totalPages := 0
		if total > 0 {
			totalPages = int(math.Ceil(float64(total) / float64(filters.PageSize)))
		}

		resp := systemLogQueryResponse{
			GeneratedAt: time.Now().UTC(),
			From:        filters.From.UTC(),
			To:          filters.To.UTC(),
			Page:        filters.Page,
			PageSize:    filters.PageSize,
			Total:       total,
			TotalPages:  totalPages,
			HasMore:     filters.Page*filters.PageSize < total,
			Sort:        filters.Sort,
			Filters: systemLogAppliedFilters{
				ActorTypes:  filters.ActorTypes,
				ActorUserID: filters.ActorUserID,
				TargetType:  filters.TargetType,
				TargetID:    filters.TargetID,
				Action:      filters.Action,
				Result:      filters.Result,
			},
			Items: buildSystemLogItems(rows),
		}

		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleGetSystemLogSummary(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		filters, err := parseSystemLogQueryFilters(c, time.Now())
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid query filters")
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
			GeneratedAt: time.Now().UTC(),
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

		items := buildSystemLogItems([]*postgresql.SystemLog{row})
		if len(items) == 0 {
			return harukiAPIHelper.ErrorNotFound(c, "system log not found")
		}
		return harukiAPIHelper.SuccessResponse(c, "success", &items[0])
	}
}

func parseSystemLogExportLimit(raw string) (int, error) {
	limit, err := parsePositiveInt(raw, defaultSystemLogExportSize, "limit")
	if err != nil {
		return 0, err
	}
	if limit > maxSystemLogExportSize {
		return 0, fiber.NewError(fiber.StatusBadRequest, "limit exceeds max allowed size")
	}
	return limit, nil
}

func buildSystemLogCSV(rows []systemLogListItem) ([]byte, error) {
	buffer := &bytes.Buffer{}
	writer := csv.NewWriter(buffer)

	header := []string{"id", "event_time", "actor_user_id", "actor_role", "actor_type", "action", "target_type", "target_id", "result", "ip", "user_agent", "method", "path", "request_id", "metadata"}
	if err := writer.Write(header); err != nil {
		return nil, err
	}

	for _, row := range rows {
		metadataStr := ""
		if row.Metadata != nil {
			data, err := json.Marshal(row.Metadata)
			if err != nil {
				return nil, err
			}
			metadataStr = string(data)
		}

		record := []string{
			strconv.Itoa(row.ID),
			row.EventTime.Format(time.RFC3339),
			row.ActorUserID,
			row.ActorRole,
			row.ActorType,
			row.Action,
			row.TargetType,
			row.TargetID,
			row.Result,
			row.IP,
			row.UserAgent,
			row.Method,
			row.Path,
			row.RequestID,
			metadataStr,
		}
		if err := writer.Write(record); err != nil {
			return nil, err
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func handleExportSystemLogs(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		filters, err := parseSystemLogQueryFilters(c, time.Now())
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid query filters")
		}

		limit, err := parseSystemLogExportLimit(c.Query("limit"))
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid export limit")
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

		items := buildSystemLogItems(rows)
		if format == "csv" {
			data, err := buildSystemLogCSV(items)
			if err != nil {
				return harukiAPIHelper.ErrorInternal(c, "failed to build csv export")
			}
			fileName := "system_logs_" + time.Now().UTC().Format("20060102_150405") + ".csv"
			c.Set(fiber.HeaderContentType, "text/csv; charset=utf-8")
			c.Set(fiber.HeaderContentDisposition, `attachment; filename="`+fileName+`"`)
			return c.Status(fiber.StatusOK).Send(data)
		}

		resp := systemLogQueryResponse{
			GeneratedAt: time.Now().UTC(),
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

func registerAdminSystemLogRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router) {
	systemLogs := adminGroup.Group("/system-logs", RequireAdmin(apiHelper))
	systemLogs.Get("/", handleQuerySystemLogs(apiHelper))
	systemLogs.Get("/summary", handleGetSystemLogSummary(apiHelper))
	systemLogs.Get("/export", handleExportSystemLogs(apiHelper))
	systemLogs.Get("/:id", handleGetSystemLogDetail(apiHelper))
}

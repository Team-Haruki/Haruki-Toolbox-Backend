package user

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/systemlog"
	"math"
	"strconv"
	"strings"
	"time"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

const (
	defaultUserActivityLogPage           = 1
	defaultUserActivityLogPageSize       = 50
	maxUserActivityLogPageSize           = 200
	defaultUserActivityLogWindowHours    = 24 * 7
	maxUserActivityLogTimeRangeHours     = 24 * 90
	defaultUserActivityLogSort           = "event_time_desc"
	userActivityLogSortEventTimeDesc     = "event_time_desc"
	userActivityLogSortEventTimeAsc      = "event_time_asc"
	userActivityLogSortIDDesc            = "id_desc"
	userActivityLogSortIDAsc             = "id_asc"
	maxUserActivityLogActionFilterLength = 128
	maxUserActivityMetadataStringLength  = 256
	redactedMetadataValue                = "[redacted]"
)

var validUserActivityLogResults = []string{
	harukiAPIHelper.SystemLogResultSuccess,
	harukiAPIHelper.SystemLogResultFailure,
}

type ownActivityLogQueryFilters struct {
	From     time.Time
	To       time.Time
	Action   string
	Result   string
	Page     int
	PageSize int
	Sort     string
}

type ownActivityLogQueryAppliedFilters struct {
	Action string `json:"action,omitempty"`
	Result string `json:"result,omitempty"`
}

type ownActivityLogListItem struct {
	ID        int            `json:"id"`
	EventTime time.Time      `json:"eventTime"`
	ActorType string         `json:"actorType"`
	Action    string         `json:"action"`
	Result    string         `json:"result"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type ownActivityLogQueryResponse struct {
	GeneratedAt time.Time                         `json:"generatedAt"`
	From        time.Time                         `json:"from"`
	To          time.Time                         `json:"to"`
	Page        int                               `json:"page"`
	PageSize    int                               `json:"pageSize"`
	Total       int                               `json:"total"`
	TotalPages  int                               `json:"totalPages"`
	HasMore     bool                              `json:"hasMore"`
	Sort        string                            `json:"sort"`
	Filters     ownActivityLogQueryAppliedFilters `json:"filters"`
	Items       []ownActivityLogListItem          `json:"items"`
}

func parseOwnActivityLogPositiveInt(raw string, defaultValue int, fieldName string) (int, error) {
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

func parseOwnActivityLogFlexibleTime(raw string) (*time.Time, error) {
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
	utc := t.UTC()
	return &utc, nil
}

func resolveOwnActivityLogTimeRange(fromRaw, toRaw string, now time.Time) (time.Time, time.Time, error) {
	fromValue, err := parseOwnActivityLogFlexibleTime(fromRaw)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	toValue, err := parseOwnActivityLogFlexibleTime(toRaw)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	now = now.UTC()
	var from time.Time
	var to time.Time

	switch {
	case fromValue == nil && toValue == nil:
		to = now
		from = to.Add(-defaultUserActivityLogWindowHours * time.Hour)
	case fromValue == nil && toValue != nil:
		to = *toValue
		from = to.Add(-defaultUserActivityLogWindowHours * time.Hour)
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
	if to.Sub(from) > maxUserActivityLogTimeRangeHours*time.Hour {
		return time.Time{}, time.Time{}, fiber.NewError(fiber.StatusBadRequest, "time range exceeds max allowed window")
	}

	return from, to, nil
}

func parseOwnActivityLogResult(raw string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return "", nil
	}
	for _, v := range validUserActivityLogResults {
		if trimmed == v {
			return trimmed, nil
		}
	}
	return "", fiber.NewError(fiber.StatusBadRequest, "invalid result filter")
}

func parseOwnActivityLogSort(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultUserActivityLogSort, nil
	}

	switch trimmed {
	case userActivityLogSortEventTimeDesc, userActivityLogSortEventTimeAsc, userActivityLogSortIDDesc, userActivityLogSortIDAsc:
		return trimmed, nil
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid sort option")
	}
}

func parseOwnActivityLogQueryFilters(c fiber.Ctx, now time.Time) (*ownActivityLogQueryFilters, error) {
	from, to, err := resolveOwnActivityLogTimeRange(c.Query("from"), c.Query("to"), now)
	if err != nil {
		return nil, err
	}

	result, err := parseOwnActivityLogResult(c.Query("result"))
	if err != nil {
		return nil, err
	}

	page, err := parseOwnActivityLogPositiveInt(c.Query("page"), defaultUserActivityLogPage, "page")
	if err != nil {
		return nil, err
	}
	pageSize, err := parseOwnActivityLogPositiveInt(c.Query("page_size"), defaultUserActivityLogPageSize, "page_size")
	if err != nil {
		return nil, err
	}
	if pageSize > maxUserActivityLogPageSize {
		return nil, fiber.NewError(fiber.StatusBadRequest, "page_size exceeds max allowed size")
	}

	sortValue, err := parseOwnActivityLogSort(c.Query("sort"))
	if err != nil {
		return nil, err
	}

	action := strings.TrimSpace(c.Query("action"))
	if len(action) > maxUserActivityLogActionFilterLength {
		return nil, fiber.NewError(fiber.StatusBadRequest, "action filter is too long")
	}

	return &ownActivityLogQueryFilters{
		From:     from,
		To:       to,
		Action:   action,
		Result:   result,
		Page:     page,
		PageSize: pageSize,
		Sort:     sortValue,
	}, nil
}

func applyOwnActivityLogSort(query *postgresql.SystemLogQuery, sortValue string) *postgresql.SystemLogQuery {
	switch sortValue {
	case userActivityLogSortEventTimeAsc:
		return query.Order(systemlog.ByEventTime(sql.OrderAsc()), systemlog.ByID(sql.OrderAsc()))
	case userActivityLogSortIDDesc:
		return query.Order(systemlog.ByID(sql.OrderDesc()))
	case userActivityLogSortIDAsc:
		return query.Order(systemlog.ByID(sql.OrderAsc()))
	default:
		return query.Order(systemlog.ByEventTime(sql.OrderDesc()), systemlog.ByID(sql.OrderDesc()))
	}
}

func isSensitiveOwnActivityMetadataKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if normalized == "" {
		return false
	}

	sensitiveKeywords := []string{
		"token",
		"secret",
		"password",
		"authorization",
		"cookie",
		"session",
		"otp",
		"one_time",
		"one-time",
	}
	for _, kw := range sensitiveKeywords {
		if strings.Contains(normalized, kw) {
			return true
		}
	}
	return false
}

func truncateOwnActivityMetadataString(raw string) string {
	if len(raw) <= maxUserActivityMetadataStringLength {
		return raw
	}
	return raw[:maxUserActivityMetadataStringLength] + "..."
}

func sanitizeOwnActivityMetadataValue(key string, value any) any {
	if isSensitiveOwnActivityMetadataKey(key) {
		return redactedMetadataValue
	}

	switch typed := value.(type) {
	case map[string]any:
		return sanitizeOwnActivityMetadataMap(typed)
	case []any:
		out := make([]any, 0, len(typed))
		for _, elem := range typed {
			out = append(out, sanitizeOwnActivityMetadataValue("", elem))
		}
		return out
	case string:
		return truncateOwnActivityMetadataString(typed)
	default:
		return typed
	}
}

func sanitizeOwnActivityMetadataMap(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}

	out := make(map[string]any, len(metadata))
	for key, value := range metadata {
		out[key] = sanitizeOwnActivityMetadataValue(key, value)
	}
	return out
}

func buildOwnActivityLogItems(rows []*postgresql.SystemLog) []ownActivityLogListItem {
	items := make([]ownActivityLogListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, ownActivityLogListItem{
			ID:        row.ID,
			EventTime: row.EventTime.UTC(),
			ActorType: string(row.ActorType),
			Action:    row.Action,
			Result:    string(row.Result),
			Metadata:  sanitizeOwnActivityMetadataMap(row.Metadata),
		})
	}
	return items
}

func handleListOwnActivityLogs(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		toolboxUserID := strings.TrimSpace(c.Params("toolbox_user_id"))
		authUserID, _ := c.Locals("userID").(string)
		authUserID = strings.TrimSpace(authUserID)
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		total := 0

		defer func() {
			writeUserAuditLog(c, apiHelper, "user.activity_logs.query", result, authUserID, map[string]any{
				"reason": reason,
				"total":  total,
			})
		}()

		if toolboxUserID == "" {
			reason = "missing_user_id"
			return harukiAPIHelper.ErrorBadRequest(c, "missing toolbox_user_id")
		}
		if authUserID == "" {
			reason = "missing_user_session"
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}
		if authUserID != toolboxUserID {
			reason = "permission_denied"
			return harukiAPIHelper.ErrorForbidden(c, "you can only access your own activity logs")
		}
		if apiHelper == nil || apiHelper.DBManager == nil || apiHelper.DBManager.DB == nil {
			reason = "db_unavailable"
			return harukiAPIHelper.ErrorInternal(c, "database unavailable")
		}

		filters, err := parseOwnActivityLogQueryFilters(c, time.Now())
		if err != nil {
			reason = "invalid_query_filters"
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid query filters")
		}

		baseQuery := apiHelper.DBManager.DB.SystemLog.Query().Where(
			systemlog.EventTimeGTE(filters.From),
			systemlog.EventTimeLTE(filters.To),
			systemlog.Or(
				systemlog.ActorUserIDEQ(authUserID),
				systemlog.And(
					systemlog.TargetTypeEQ("user"),
					systemlog.TargetIDEQ(authUserID),
				),
			),
		)

		if filters.Action != "" {
			baseQuery = baseQuery.Where(systemlog.ActionContainsFold(filters.Action))
		}
		if filters.Result != "" {
			baseQuery = baseQuery.Where(systemlog.ResultEQ(systemlog.Result(filters.Result)))
		}

		total, err = baseQuery.Clone().Count(c.Context())
		if err != nil {
			reason = "count_activity_logs_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to count activity logs")
		}

		offset := (filters.Page - 1) * filters.PageSize
		rows, err := applyOwnActivityLogSort(baseQuery.Clone(), filters.Sort).
			Offset(offset).
			Limit(filters.PageSize).
			All(c.Context())
		if err != nil {
			reason = "query_activity_logs_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to query activity logs")
		}

		totalPages := 0
		if total > 0 {
			totalPages = int(math.Ceil(float64(total) / float64(filters.PageSize)))
		}
		hasMore := filters.Page < totalPages

		resp := ownActivityLogQueryResponse{
			GeneratedAt: time.Now().UTC(),
			From:        filters.From.UTC(),
			To:          filters.To.UTC(),
			Page:        filters.Page,
			PageSize:    filters.PageSize,
			Total:       total,
			TotalPages:  totalPages,
			HasMore:     hasMore,
			Sort:        filters.Sort,
			Filters: ownActivityLogQueryAppliedFilters{
				Action: filters.Action,
				Result: filters.Result,
			},
			Items: buildOwnActivityLogItems(rows),
		}

		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.SuccessResponse(c, "ok", &resp)
	}
}

func registerActivityLogRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id/activity-logs")
	r.Get("/", apiHelper.SessionHandler.VerifySessionToken, checkUserNotBanned(apiHelper), handleListOwnActivityLogs(apiHelper))
}

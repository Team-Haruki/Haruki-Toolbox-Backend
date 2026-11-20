package adminsyslog

import (
	"bytes"
	"context"
	"encoding/csv"
	json "encoding/json/v2"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	platformFiltering "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/filtering"
	platformPagination "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/pagination"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/systemlog"

	"entgo.io/ent/dialect"
	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

func summarizeSystemLogFailureReasons(ctx context.Context, query *postgresql.SystemLogQuery) ([]categoryCount, error) {
	var rows []groupedFieldCount
	err := query.Where(systemlog.ResultEQ(systemlog.ResultFailure)).
		Aggregate(groupFailureReasons, postgresql.As(postgresql.Count(), "count")).Scan(ctx, &rows)
	return normalizeCategoryCounts(rows), err
}

func groupFailureReasons(s *sql.Selector) string {
	column := s.C(systemlog.FieldMetadata)
	// A bounded text key preserves the ordinary reason codes while preventing
	// old free-form metadata from producing large groups or response entries.
	const whitespace = "' \t\n\v\f\r\u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000'"
	var reason string
	if s.Dialect() == dialect.SQLite {
		reason = "CASE WHEN json_type(" + column + ", '$.reason') = 'text' THEN trim(json_extract(" + column + ", '$.reason'), " + whitespace + ") END"
	} else {
		reason = "CASE WHEN jsonb_typeof(" + column + "::jsonb -> 'reason') = 'string' THEN btrim(" + column + "::jsonb ->> 'reason', " + whitespace + ") END"
	}
	s.GroupBy("key").OrderExpr(sql.Expr("count DESC, key ASC")).Limit(maxFailureReasonGroups)
	return sql.As("COALESCE(NULLIF(substr("+reason+", 1, 128), ''), '"+unknownSystemLogFailureReason+"')", "key")
}

func parseSystemLogActorTypesFilter(raw string) ([]string, error) {
	values := platformFiltering.ParseCSVValues(raw)
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
	page, pageSize, err := platformPagination.ParsePageAndPageSize(c, defaultSystemLogPage, defaultSystemLogPageSize, maxSystemLogPageSize)
	if err != nil {
		return nil, err
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

func parseSystemLogExportLimit(raw string) (int, error) {
	limit, err := platformPagination.ParsePositiveInt(raw, defaultSystemLogExportSize, "limit")
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
		for i, value := range record {
			trimmed := strings.TrimLeftFunc(value, unicode.IsSpace)
			if strings.HasPrefix(value, "\t") || strings.HasPrefix(value, "\r") ||
				strings.HasPrefix(value, "\n") || (len(trimmed) > 0 && strings.ContainsRune("=+-@", rune(trimmed[0]))) {
				record[i] = "'" + value
			}
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

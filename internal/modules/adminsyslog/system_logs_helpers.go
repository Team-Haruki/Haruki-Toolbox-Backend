package adminsyslog

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	platformFiltering "haruki-suite/internal/platform/filtering"
	platformPagination "haruki-suite/internal/platform/pagination"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/systemlog"
	"slices"
	"strconv"
	"strings"
	"time"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

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

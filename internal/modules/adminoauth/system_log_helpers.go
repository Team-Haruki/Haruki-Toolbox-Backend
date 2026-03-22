package adminoauth

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	platformFiltering "haruki-suite/internal/platform/filtering"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/systemlog"
	"slices"
	"sort"
	"strings"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

const (
	defaultSystemLogPage          = 1
	defaultSystemLogPageSize      = 50
	maxSystemLogPageSize          = 200
	defaultSystemLogSort          = "event_time_desc"
	systemLogSortEventTimeAsc     = "event_time_asc"
	systemLogSortEventTimeDesc    = "event_time_desc"
	systemLogSortIDAsc            = "id_asc"
	systemLogSortIDDesc           = "id_desc"
	unknownSystemLogFailureReason = "unknown"
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

type categoryCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type groupedFieldCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type systemLogListItem = adminCoreModule.SystemLogListItem

func normalizeCategoryCounts(rows []groupedFieldCount) []categoryCount {
	out := make([]categoryCount, 0, len(rows))
	for _, row := range rows {
		out = append(out, categoryCount(row))
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Key < out[j].Key
		}
		return out[i].Count > out[j].Count
	})

	return out
}

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

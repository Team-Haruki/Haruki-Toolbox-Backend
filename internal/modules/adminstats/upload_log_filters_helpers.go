package adminstats

import (
	platformFiltering "haruki-suite/internal/platform/filtering"
	platformPagination "haruki-suite/internal/platform/pagination"
	platformTime "haruki-suite/internal/platform/timeutil"
	harukiUtils "haruki-suite/utils"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/uploadlog"
	"slices"
	"strconv"
	"strings"
	"time"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

func parseCSVValues(raw string) []string {
	return platformFiltering.ParseCSVValues(raw)
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

func resolveUploadLogTimeRange(fromRaw, toRaw string, now time.Time) (time.Time, time.Time, error) {
	return platformTime.ResolveUploadLogTimeRange(fromRaw, toRaw, now)
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

	page, pageSize, err := platformPagination.ParsePageAndPageSize(c, defaultUploadLogPage, defaultUploadLogPageSize, maxUploadLogPageSize)
	if err != nil {
		return nil, err
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

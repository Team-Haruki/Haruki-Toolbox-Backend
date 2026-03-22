package adminusers

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	platformPagination "haruki-suite/internal/platform/pagination"
	platformTime "haruki-suite/internal/platform/timeutil"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"strings"
	"time"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

func parseAdminUsersSort(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultAdminUsersSort, nil
	}

	switch trimmed {
	case adminUsersSortIDDesc, adminUsersSortIDAsc, adminUsersSortNameDesc, adminUsersSortNameAsc, adminUsersSortCreatedAtDesc, adminUsersSortCreatedAtAsc:
		return trimmed, nil
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid sort option")
	}
}

func parseAdminUserQueryFilters(c fiber.Ctx) (*adminUserQueryFilters, error) {
	queryValue := strings.TrimSpace(c.Query("q"))
	roleValue := strings.TrimSpace(c.Query("role"))
	if roleValue != "" {
		roleValue = adminCoreModule.NormalizeRole(roleValue)
		if !adminCoreModule.IsValidRole(roleValue) {
			return nil, fiber.NewError(fiber.StatusBadRequest, "invalid role filter")
		}
	}

	banned, err := adminCoreModule.ParseOptionalBoolField(c.Query("banned"), "banned")
	if err != nil {
		return nil, err
	}
	allowCNMysekaiRaw := strings.TrimSpace(c.Query("allow_cn_mysekai"))
	if allowCNMysekaiRaw == "" {
		allowCNMysekaiRaw = strings.TrimSpace(c.Query("allowCNMysekai"))
	}
	allowCNMysekai, err := adminCoreModule.ParseOptionalBoolField(allowCNMysekaiRaw, "allow_cn_mysekai")
	if err != nil {
		return nil, err
	}
	createdFrom, err := platformTime.ParseFlexibleTime(c.Query("created_from"))
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid created_from filter")
	}
	createdTo, err := platformTime.ParseFlexibleTime(c.Query("created_to"))
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid created_to filter")
	}
	if createdFrom != nil && createdTo != nil && createdFrom.After(*createdTo) {
		return nil, fiber.NewError(fiber.StatusBadRequest, "created_from must be earlier than or equal to created_to")
	}

	page, pageSize, err := platformPagination.ParsePageAndPageSize(c, defaultAdminUsersPage, defaultAdminUsersPageSize, maxAdminUsersPageSize)
	if err != nil {
		return nil, err
	}

	sortValue, err := parseAdminUsersSort(c.Query("sort"))
	if err != nil {
		return nil, err
	}

	return &adminUserQueryFilters{
		Query:          queryValue,
		Role:           roleValue,
		Banned:         banned,
		AllowCNMysekai: allowCNMysekai,
		CreatedFrom:    createdFrom,
		CreatedTo:      createdTo,
		Page:           page,
		PageSize:       pageSize,
		Sort:           sortValue,
	}, nil
}

func applyAdminUserQueryFilters(query *postgresql.UserQuery, filters *adminUserQueryFilters) *postgresql.UserQuery {
	q := query
	if filters.Query != "" {
		q = q.Where(userSchema.Or(
			userSchema.IDContainsFold(filters.Query),
			userSchema.NameContainsFold(filters.Query),
			userSchema.EmailContainsFold(filters.Query),
		))
	}
	if filters.Role != "" {
		q = q.Where(userSchema.RoleEQ(userSchema.Role(filters.Role)))
	}
	if filters.Banned != nil {
		q = q.Where(userSchema.BannedEQ(*filters.Banned))
	}
	if filters.AllowCNMysekai != nil {
		q = q.Where(userSchema.AllowCnMysekaiEQ(*filters.AllowCNMysekai))
	}
	if filters.CreatedFrom != nil {
		q = q.Where(
			userSchema.CreatedAtNotNil(),
			userSchema.CreatedAtGTE(filters.CreatedFrom.UTC()),
		)
	}
	if filters.CreatedTo != nil {
		q = q.Where(
			userSchema.CreatedAtNotNil(),
			userSchema.CreatedAtLTE(filters.CreatedTo.UTC()),
		)
	}
	return q
}

func applyAdminUsersSort(query *postgresql.UserQuery, sortValue string) *postgresql.UserQuery {
	switch sortValue {
	case adminUsersSortIDAsc:
		return query.Order(userSchema.ByID(sql.OrderAsc()))
	case adminUsersSortNameDesc:
		return query.Order(userSchema.ByName(sql.OrderDesc()), userSchema.ByID(sql.OrderDesc()))
	case adminUsersSortNameAsc:
		return query.Order(userSchema.ByName(sql.OrderAsc()), userSchema.ByID(sql.OrderAsc()))
	case adminUsersSortCreatedAtDesc:
		return query.Order(userSchema.ByCreatedAt(sql.OrderDesc()), userSchema.ByID(sql.OrderDesc()))
	case adminUsersSortCreatedAtAsc:
		return query.Order(userSchema.ByCreatedAt(sql.OrderAsc()), userSchema.ByID(sql.OrderAsc()))
	default:
		return query.Order(userSchema.ByID(sql.OrderDesc()))
	}
}

func buildAdminUserListItems(rows []*postgresql.User) []adminUserListItem {
	items := make([]adminUserListItem, 0, len(rows))
	for _, row := range rows {
		var createdAt *time.Time
		if row.CreatedAt != nil {
			createdAtUTC := row.CreatedAt.UTC()
			createdAt = &createdAtUTC
		}
		items = append(items, adminUserListItem{
			UserID:         row.ID,
			Name:           row.Name,
			Email:          row.Email,
			Role:           adminCoreModule.NormalizeRole(string(row.Role)),
			Banned:         row.Banned,
			AllowCNMysekai: row.AllowCnMysekai,
			BanReason:      row.BanReason,
			CreatedAt:      createdAt,
		})
	}
	return items
}

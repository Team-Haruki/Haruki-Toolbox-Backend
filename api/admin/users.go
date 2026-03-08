package admin

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"math"
	"strconv"
	"strings"
	"time"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

const (
	defaultAdminUsersPage     = 1
	defaultAdminUsersPageSize = 50
	maxAdminUsersPageSize     = 200
	defaultAdminUsersSort     = "id_desc"

	adminUsersSortIDDesc        = "id_desc"
	adminUsersSortIDAsc         = "id_asc"
	adminUsersSortNameDesc      = "name_desc"
	adminUsersSortNameAsc       = "name_asc"
	adminUsersSortCreatedAtDesc = "created_at_desc"
	adminUsersSortCreatedAtAsc  = "created_at_asc"
)

type adminUserQueryFilters struct {
	Query       string
	Role        string
	Banned      *bool
	CreatedFrom *time.Time
	CreatedTo   *time.Time
	Page        int
	PageSize    int
	Sort        string
}

type adminUserListItem struct {
	UserID    string     `json:"userId"`
	Name      string     `json:"name"`
	Email     string     `json:"email"`
	Role      string     `json:"role"`
	Banned    bool       `json:"banned"`
	BanReason *string    `json:"banReason,omitempty"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
}

type adminUserAppliedFilters struct {
	Query       string     `json:"q,omitempty"`
	Role        string     `json:"role,omitempty"`
	Banned      *bool      `json:"banned,omitempty"`
	CreatedFrom *time.Time `json:"createdFrom,omitempty"`
	CreatedTo   *time.Time `json:"createdTo,omitempty"`
}

type adminUserListResponse struct {
	GeneratedAt time.Time               `json:"generatedAt"`
	Page        int                     `json:"page"`
	PageSize    int                     `json:"pageSize"`
	Total       int                     `json:"total"`
	TotalPages  int                     `json:"totalPages"`
	HasMore     bool                    `json:"hasMore"`
	Sort        string                  `json:"sort"`
	Filters     adminUserAppliedFilters `json:"filters"`
	Items       []adminUserListItem     `json:"items"`
}

type updateUserBanPayload struct {
	Reason *string `json:"reason"`
}

type userBanStatusResponse struct {
	UserID    string  `json:"userId"`
	Role      string  `json:"role"`
	Banned    bool    `json:"banned"`
	BanReason *string `json:"banReason,omitempty"`
}

func parseOptionalBoolField(raw, fieldName string) (*bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}

	v, err := strconv.ParseBool(trimmed)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid "+fieldName+" filter")
	}
	return &v, nil
}

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
		roleValue = normalizeRole(roleValue)
		if !isValidRole(roleValue) {
			return nil, fiber.NewError(fiber.StatusBadRequest, "invalid role filter")
		}
	}

	banned, err := parseOptionalBoolField(c.Query("banned"), "banned")
	if err != nil {
		return nil, err
	}
	createdFrom, err := parseFlexibleTime(c.Query("created_from"))
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid created_from filter")
	}
	createdTo, err := parseFlexibleTime(c.Query("created_to"))
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid created_to filter")
	}
	if createdFrom != nil && createdTo != nil && createdFrom.After(*createdTo) {
		return nil, fiber.NewError(fiber.StatusBadRequest, "created_from must be earlier than or equal to created_to")
	}

	page, err := parsePositiveInt(c.Query("page"), defaultAdminUsersPage, "page")
	if err != nil {
		return nil, err
	}
	pageSize, err := parsePositiveInt(c.Query("page_size"), defaultAdminUsersPageSize, "page_size")
	if err != nil {
		return nil, err
	}
	if pageSize > maxAdminUsersPageSize {
		return nil, fiber.NewError(fiber.StatusBadRequest, "page_size exceeds max allowed size")
	}

	sortValue, err := parseAdminUsersSort(c.Query("sort"))
	if err != nil {
		return nil, err
	}

	return &adminUserQueryFilters{
		Query:       queryValue,
		Role:        roleValue,
		Banned:      banned,
		CreatedFrom: createdFrom,
		CreatedTo:   createdTo,
		Page:        page,
		PageSize:    pageSize,
		Sort:        sortValue,
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
			UserID:    row.ID,
			Name:      row.Name,
			Email:     row.Email,
			Role:      normalizeRole(string(row.Role)),
			Banned:    row.Banned,
			BanReason: row.BanReason,
			CreatedAt: createdAt,
		})
	}
	return items
}

func currentAdminActor(c fiber.Ctx) (string, string, error) {
	userID, ok := c.Locals("userID").(string)
	if !ok || strings.TrimSpace(userID) == "" {
		return "", "", fiber.NewError(fiber.StatusUnauthorized, "missing user session")
	}

	role, ok := c.Locals("userRole").(string)
	normalizedRole := normalizeRole(role)
	if !ok || !isValidRole(normalizedRole) {
		return "", "", fiber.NewError(fiber.StatusUnauthorized, "missing user role")
	}

	return userID, normalizedRole, nil
}

func ensureAdminCanManageTargetUser(actorUserID, actorRole, targetUserID, targetRole string) error {
	if actorUserID == targetUserID {
		return fiber.NewError(fiber.StatusBadRequest, "cannot manage current user")
	}

	if normalizeRole(actorRole) != roleSuperAdmin && normalizeRole(targetRole) == roleSuperAdmin {
		return fiber.NewError(fiber.StatusForbidden, "only super admin can manage super admin users")
	}
	return nil
}

func sanitizeBanReason(raw *string) (*string, error) {
	if raw == nil {
		return nil, nil
	}

	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil, nil
	}
	if len(trimmed) > 500 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "ban reason exceeds max length")
	}

	reason := trimmed
	return &reason, nil
}

func writeAdminAuditLog(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, action string, targetType string, targetID string, result string, metadata map[string]any) {
	var targetTypePtr *string
	if normalizedTargetType := strings.TrimSpace(targetType); normalizedTargetType != "" {
		targetTypeCopy := normalizedTargetType
		targetTypePtr = &targetTypeCopy
	}

	var targetIDPtr *string
	if normalizedTargetID := strings.TrimSpace(targetID); normalizedTargetID != "" {
		targetIDCopy := normalizedTargetID
		targetIDPtr = &targetIDCopy
	}

	entry := harukiAPIHelper.BuildSystemLogEntryFromFiber(c, action, result, targetTypePtr, targetIDPtr, metadata)
	_ = harukiAPIHelper.WriteSystemLog(c.Context(), apiHelper, entry)
}

func adminFailureMetadata(reason string, extra map[string]any) map[string]any {
	out := make(map[string]any, 1+len(extra))
	out["reason"] = reason
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func handleListUsers(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		filters, err := parseAdminUserQueryFilters(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid query filters")
		}

		dbCtx := c.Context()
		baseQuery := applyAdminUserQueryFilters(apiHelper.DBManager.DB.User.Query(), filters)

		total, err := baseQuery.Clone().Count(dbCtx)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to count users")
		}

		offset := (filters.Page - 1) * filters.PageSize
		rows, err := applyAdminUsersSort(baseQuery.Clone(), filters.Sort).
			Limit(filters.PageSize).
			Offset(offset).
			Select(
				userSchema.FieldID,
				userSchema.FieldName,
				userSchema.FieldEmail,
				userSchema.FieldRole,
				userSchema.FieldBanned,
				userSchema.FieldBanReason,
				userSchema.FieldCreatedAt,
			).
			All(dbCtx)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "failed to query users")
		}

		totalPages := 0
		if total > 0 {
			totalPages = int(math.Ceil(float64(total) / float64(filters.PageSize)))
		}

		resp := adminUserListResponse{
			GeneratedAt: time.Now().UTC(),
			Page:        filters.Page,
			PageSize:    filters.PageSize,
			Total:       total,
			TotalPages:  totalPages,
			HasMore:     filters.Page*filters.PageSize < total,
			Sort:        filters.Sort,
			Filters: adminUserAppliedFilters{
				Query:       filters.Query,
				Role:        filters.Role,
				Banned:      filters.Banned,
				CreatedFrom: filters.CreatedFrom,
				CreatedTo:   filters.CreatedTo,
			},
			Items: buildAdminUserListItems(rows),
		}

		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleBanUser(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetUserID := strings.TrimSpace(c.Params("target_user_id"))
		if targetUserID == "" {
			writeAdminAuditLog(c, apiHelper, "admin.user.ban", "user", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_target_user_id", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "target_user_id is required")
		}

		actorUserID, actorRole, err := currentAdminActor(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.ban", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_user_session", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		var payload updateUserBanPayload
		if len(c.Body()) > 0 {
			if err := c.Bind().Body(&payload); err != nil {
				writeAdminAuditLog(c, apiHelper, "admin.user.ban", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
				return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
			}
		}

		reason, err := sanitizeBanReason(payload.Reason)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.ban", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_ban_reason", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid ban reason")
		}

		targetUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(targetUserID)).
			Select(
				userSchema.FieldID,
				userSchema.FieldRole,
				userSchema.FieldBanned,
				userSchema.FieldBanReason,
			).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, "admin.user.ban", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("target_user_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			writeAdminAuditLog(c, apiHelper, "admin.user.ban", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_target_user_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}

		if err := ensureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.ban", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("permission_denied", map[string]any{
				"actorRole":  actorRole,
				"targetRole": normalizeRole(string(targetUser.Role)),
			}))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorForbidden(c, "insufficient permissions")
		}

		update := apiHelper.DBManager.DB.User.UpdateOneID(targetUser.ID).SetBanned(true)
		if reason != nil {
			update.SetBanReason(*reason)
		} else {
			update.ClearBanReason()
		}

		updatedUser, err := update.Save(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, "admin.user.ban", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("target_user_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			writeAdminAuditLog(c, apiHelper, "admin.user.ban", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("ban_user_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to ban user")
		}

		resp := userBanStatusResponse{
			UserID:    updatedUser.ID,
			Role:      normalizeRole(string(updatedUser.Role)),
			Banned:    updatedUser.Banned,
			BanReason: updatedUser.BanReason,
		}
		writeAdminAuditLog(c, apiHelper, "admin.user.ban", "user", updatedUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"hasReason": reason != nil,
		})
		return harukiAPIHelper.SuccessResponse(c, "user banned", &resp)
	}
}

func handleUnbanUser(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetUserID := strings.TrimSpace(c.Params("target_user_id"))
		if targetUserID == "" {
			writeAdminAuditLog(c, apiHelper, "admin.user.unban", "user", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_target_user_id", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "target_user_id is required")
		}

		actorUserID, actorRole, err := currentAdminActor(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.unban", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_user_session", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		targetUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(targetUserID)).
			Select(userSchema.FieldID, userSchema.FieldRole).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, "admin.user.unban", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("target_user_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			writeAdminAuditLog(c, apiHelper, "admin.user.unban", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_target_user_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}

		if err := ensureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.unban", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("permission_denied", map[string]any{
				"actorRole":  actorRole,
				"targetRole": normalizeRole(string(targetUser.Role)),
			}))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorForbidden(c, "insufficient permissions")
		}

		updatedUser, err := apiHelper.DBManager.DB.User.UpdateOneID(targetUser.ID).
			SetBanned(false).
			ClearBanReason().
			Save(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, "admin.user.unban", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("target_user_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			writeAdminAuditLog(c, apiHelper, "admin.user.unban", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("unban_user_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to unban user")
		}

		resp := userBanStatusResponse{
			UserID: updatedUser.ID,
			Role:   normalizeRole(string(updatedUser.Role)),
			Banned: updatedUser.Banned,
		}
		writeAdminAuditLog(c, apiHelper, "admin.user.unban", "user", updatedUser.ID, harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse(c, "user unbanned", &resp)
	}
}

package admin

import (
	"haruki-suite/entsrc/schema"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"math"
	"strconv"
	"strings"
	"time"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

const (
	defaultAdminGlobalGameBindingPage     = 1
	defaultAdminGlobalGameBindingPageSize = 50
	maxAdminGlobalGameBindingPageSize     = 200
	defaultAdminGlobalGameBindingSort     = "id_desc"
	maxBatchGameBindingOperationCount     = 200

	adminGlobalGameBindingSortIDDesc         = "id_desc"
	adminGlobalGameBindingSortIDAsc          = "id_asc"
	adminGlobalGameBindingSortGameUserIDDesc = "game_user_id_desc"
	adminGlobalGameBindingSortGameUserIDAsc  = "game_user_id_asc"
	adminGlobalGameBindingSortUserIDDesc     = "user_id_desc"
	adminGlobalGameBindingSortUserIDAsc      = "user_id_asc"
)

type adminGlobalGameBindingQueryFilters struct {
	Query      string
	Server     string
	GameUserID string
	UserID     string
	Verified   *bool
	Page       int
	PageSize   int
	Sort       string
}

type adminGlobalGameBindingOwner struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

type adminGlobalGameBindingItem struct {
	ID         int                                `json:"id"`
	Server     string                             `json:"server"`
	GameUserID string                             `json:"gameUserId"`
	Verified   bool                               `json:"verified"`
	Suite      *schema.SuiteDataPrivacySettings   `json:"suite,omitempty"`
	Mysekai    *schema.MysekaiDataPrivacySettings `json:"mysekai,omitempty"`
	Owner      adminGlobalGameBindingOwner        `json:"owner"`
}

type adminGlobalGameBindingAppliedFilters struct {
	Query      string `json:"q,omitempty"`
	Server     string `json:"server,omitempty"`
	GameUserID string `json:"gameUserId,omitempty"`
	UserID     string `json:"userId,omitempty"`
	Verified   *bool  `json:"verified,omitempty"`
}

type adminGlobalGameBindingListResponse struct {
	GeneratedAt time.Time                            `json:"generatedAt"`
	Page        int                                  `json:"page"`
	PageSize    int                                  `json:"pageSize"`
	Total       int                                  `json:"total"`
	TotalPages  int                                  `json:"totalPages"`
	HasMore     bool                                 `json:"hasMore"`
	Sort        string                               `json:"sort"`
	Filters     adminGlobalGameBindingAppliedFilters `json:"filters"`
	Items       []adminGlobalGameBindingItem         `json:"items"`
}

type adminGlobalGameBindingReassignPayload struct {
	TargetUserID      string `json:"targetUserId"`
	TargetUserIDSnake string `json:"target_user_id"`
}

type adminGlobalGameBindingReassignResponse struct {
	Server       string `json:"server"`
	GameUserID   string `json:"gameUserId"`
	FromUserID   string `json:"fromUserId"`
	TargetUserID string `json:"targetUserId"`
	Changed      bool   `json:"changed"`
}

type adminGlobalGameBindingRef struct {
	Server          string `json:"server"`
	GameUserID      string `json:"gameUserId"`
	GameUserIDSnake string `json:"game_user_id"`
}

type adminGlobalGameBindingBatchDeletePayload struct {
	Items []adminGlobalGameBindingRef `json:"items"`
}

type adminGlobalGameBindingBatchDeleteItemResult struct {
	Server     string `json:"server"`
	GameUserID string `json:"gameUserId"`
	Success    bool   `json:"success"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message,omitempty"`
}

type adminGlobalGameBindingBatchDeleteResponse struct {
	Total   int                                           `json:"total"`
	Success int                                           `json:"success"`
	Failed  int                                           `json:"failed"`
	Results []adminGlobalGameBindingBatchDeleteItemResult `json:"results"`
}

type adminGlobalGameBindingBatchReassignItem struct {
	Server            string `json:"server"`
	GameUserID        string `json:"gameUserId"`
	GameUserIDSnake   string `json:"game_user_id"`
	TargetUserID      string `json:"targetUserId"`
	TargetUserIDSnake string `json:"target_user_id"`
}

type adminGlobalGameBindingBatchReassignPayload struct {
	Items []adminGlobalGameBindingBatchReassignItem `json:"items"`
}

type adminGlobalGameBindingBatchReassignItemResult struct {
	Server       string `json:"server"`
	GameUserID   string `json:"gameUserId"`
	FromUserID   string `json:"fromUserId,omitempty"`
	TargetUserID string `json:"targetUserId,omitempty"`
	Changed      bool   `json:"changed,omitempty"`
	Success      bool   `json:"success"`
	Code         string `json:"code,omitempty"`
	Message      string `json:"message,omitempty"`
}

type adminGlobalGameBindingBatchReassignResponse struct {
	Total   int                                             `json:"total"`
	Success int                                             `json:"success"`
	Failed  int                                             `json:"failed"`
	Results []adminGlobalGameBindingBatchReassignItemResult `json:"results"`
}

func parseAdminGlobalGameBindingSort(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultAdminGlobalGameBindingSort, nil
	}

	switch trimmed {
	case adminGlobalGameBindingSortIDDesc,
		adminGlobalGameBindingSortIDAsc,
		adminGlobalGameBindingSortGameUserIDDesc,
		adminGlobalGameBindingSortGameUserIDAsc,
		adminGlobalGameBindingSortUserIDDesc,
		adminGlobalGameBindingSortUserIDAsc:
		return trimmed, nil
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid sort option")
	}
}

func parseAdminGlobalGameBindingQueryFilters(c fiber.Ctx) (*adminGlobalGameBindingQueryFilters, error) {
	queryValue := strings.TrimSpace(c.Query("q"))
	serverRaw := strings.TrimSpace(c.Query("server"))
	gameUserID := strings.TrimSpace(c.Query("game_user_id"))
	userID := strings.TrimSpace(c.Query("user_id"))

	serverValue := ""
	if serverRaw != "" {
		server, err := harukiUtils.ParseSupportedDataUploadServer(serverRaw)
		if err != nil {
			return nil, fiber.NewError(fiber.StatusBadRequest, "invalid server filter")
		}
		serverValue = string(server)
	}
	if gameUserID != "" {
		if _, err := strconv.Atoi(gameUserID); err != nil {
			return nil, fiber.NewError(fiber.StatusBadRequest, "game_user_id must be numeric")
		}
	}

	verified, err := parseOptionalBoolField(c.Query("verified"), "verified")
	if err != nil {
		return nil, err
	}
	page, err := parsePositiveInt(c.Query("page"), defaultAdminGlobalGameBindingPage, "page")
	if err != nil {
		return nil, err
	}
	pageSize, err := parsePositiveInt(c.Query("page_size"), defaultAdminGlobalGameBindingPageSize, "page_size")
	if err != nil {
		return nil, err
	}
	if pageSize > maxAdminGlobalGameBindingPageSize {
		return nil, fiber.NewError(fiber.StatusBadRequest, "page_size exceeds max allowed size")
	}

	sortValue, err := parseAdminGlobalGameBindingSort(c.Query("sort"))
	if err != nil {
		return nil, err
	}

	return &adminGlobalGameBindingQueryFilters{
		Query:      queryValue,
		Server:     serverValue,
		GameUserID: gameUserID,
		UserID:     userID,
		Verified:   verified,
		Page:       page,
		PageSize:   pageSize,
		Sort:       sortValue,
	}, nil
}

func applyAdminGlobalGameBindingFilters(query *postgresql.GameAccountBindingQuery, filters *adminGlobalGameBindingQueryFilters, actorRole string) *postgresql.GameAccountBindingQuery {
	q := query
	if normalizeRole(actorRole) != roleSuperAdmin {
		q = q.Where(gameaccountbinding.HasUserWith(userSchema.RoleNEQ(userSchema.RoleSuperAdmin)))
	}
	if filters.Query != "" {
		q = q.Where(gameaccountbinding.Or(
			gameaccountbinding.GameUserIDContainsFold(filters.Query),
			gameaccountbinding.HasUserWith(userSchema.Or(
				userSchema.IDContainsFold(filters.Query),
				userSchema.NameContainsFold(filters.Query),
				userSchema.EmailContainsFold(filters.Query),
			)),
		))
	}
	if filters.Server != "" {
		q = q.Where(gameaccountbinding.ServerEQ(filters.Server))
	}
	if filters.GameUserID != "" {
		q = q.Where(gameaccountbinding.GameUserIDEQ(filters.GameUserID))
	}
	if filters.UserID != "" {
		q = q.Where(gameaccountbinding.HasUserWith(userSchema.IDEQ(filters.UserID)))
	}
	if filters.Verified != nil {
		q = q.Where(gameaccountbinding.VerifiedEQ(*filters.Verified))
	}
	return q
}

func applyAdminGlobalGameBindingSort(query *postgresql.GameAccountBindingQuery, sortValue string) *postgresql.GameAccountBindingQuery {
	switch sortValue {
	case adminGlobalGameBindingSortIDAsc:
		return query.Order(gameaccountbinding.ByID(sql.OrderAsc()))
	case adminGlobalGameBindingSortGameUserIDDesc:
		return query.Order(gameaccountbinding.ByGameUserID(sql.OrderDesc()), gameaccountbinding.ByID(sql.OrderDesc()))
	case adminGlobalGameBindingSortGameUserIDAsc:
		return query.Order(gameaccountbinding.ByGameUserID(sql.OrderAsc()), gameaccountbinding.ByID(sql.OrderAsc()))
	case adminGlobalGameBindingSortUserIDDesc:
		return query.Order(gameaccountbinding.ByUserField(userSchema.FieldID, sql.OrderDesc()), gameaccountbinding.ByID(sql.OrderDesc()))
	case adminGlobalGameBindingSortUserIDAsc:
		return query.Order(gameaccountbinding.ByUserField(userSchema.FieldID, sql.OrderAsc()), gameaccountbinding.ByID(sql.OrderAsc()))
	default:
		return query.Order(gameaccountbinding.ByID(sql.OrderDesc()))
	}
}

func buildAdminGlobalGameBindingItems(rows []*postgresql.GameAccountBinding) []adminGlobalGameBindingItem {
	items := make([]adminGlobalGameBindingItem, 0, len(rows))
	for _, row := range rows {
		item := adminGlobalGameBindingItem{
			ID:         row.ID,
			Server:     row.Server,
			GameUserID: row.GameUserID,
			Verified:   row.Verified,
			Suite:      row.Suite,
			Mysekai:    row.Mysekai,
		}
		if row.Edges.User != nil {
			item.Owner = adminGlobalGameBindingOwner{
				UserID: row.Edges.User.ID,
				Name:   row.Edges.User.Name,
				Email:  row.Edges.User.Email,
				Role:   normalizeRole(string(row.Edges.User.Role)),
			}
		}
		items = append(items, item)
	}
	return items
}

func parseAdminGameBindingPath(c fiber.Ctx) (string, string, error) {
	serverRaw := strings.TrimSpace(c.Params("server"))
	server, err := harukiUtils.ParseSupportedDataUploadServer(serverRaw)
	if err != nil {
		return "", "", fiber.NewError(fiber.StatusBadRequest, "invalid server")
	}

	gameUserID := strings.TrimSpace(c.Params("game_user_id"))
	if gameUserID == "" {
		return "", "", fiber.NewError(fiber.StatusBadRequest, "game_user_id is required")
	}
	if _, err := strconv.Atoi(gameUserID); err != nil {
		return "", "", fiber.NewError(fiber.StatusBadRequest, "game_user_id must be numeric")
	}
	return string(server), gameUserID, nil
}

func parseAdminGameBindingReassignPayload(c fiber.Ctx) (string, error) {
	var payload adminGlobalGameBindingReassignPayload
	if err := c.Bind().Body(&payload); err != nil {
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	targetUserID := strings.TrimSpace(payload.TargetUserID)
	if targetUserID == "" {
		targetUserID = strings.TrimSpace(payload.TargetUserIDSnake)
	}
	if targetUserID == "" {
		return "", fiber.NewError(fiber.StatusBadRequest, "targetUserId is required")
	}
	return targetUserID, nil
}

func parseAdminGameBindingRef(ref adminGlobalGameBindingRef) (string, string, error) {
	serverRaw := strings.TrimSpace(ref.Server)
	server, err := harukiUtils.ParseSupportedDataUploadServer(serverRaw)
	if err != nil {
		return "", "", fiber.NewError(fiber.StatusBadRequest, "invalid server")
	}

	gameUserID := strings.TrimSpace(ref.GameUserID)
	if gameUserID == "" {
		gameUserID = strings.TrimSpace(ref.GameUserIDSnake)
	}
	if gameUserID == "" {
		return "", "", fiber.NewError(fiber.StatusBadRequest, "gameUserId is required")
	}
	if _, err := strconv.Atoi(gameUserID); err != nil {
		return "", "", fiber.NewError(fiber.StatusBadRequest, "gameUserId must be numeric")
	}

	return string(server), gameUserID, nil
}

func sanitizeAdminBatchGameBindingRefs(raw []adminGlobalGameBindingRef) ([]adminGlobalGameBindingRef, error) {
	if len(raw) == 0 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "items is required")
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]adminGlobalGameBindingRef, 0, len(raw))
	for _, ref := range raw {
		server, gameUserID, err := parseAdminGameBindingRef(ref)
		if err != nil {
			return nil, err
		}
		key := server + ":" + gameUserID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, adminGlobalGameBindingRef{
			Server:     server,
			GameUserID: gameUserID,
		})
	}

	if len(out) == 0 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "items is required")
	}
	if len(out) > maxBatchGameBindingOperationCount {
		return nil, fiber.NewError(fiber.StatusBadRequest, "too many items in one batch")
	}
	return out, nil
}

func sanitizeAdminBatchGameBindingReassignItems(raw []adminGlobalGameBindingBatchReassignItem) ([]adminGlobalGameBindingBatchReassignItem, error) {
	if len(raw) == 0 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "items is required")
	}

	type dedupeValue struct {
		server       string
		gameUserID   string
		targetUserID string
	}
	seen := make(map[string]dedupeValue, len(raw))
	out := make([]adminGlobalGameBindingBatchReassignItem, 0, len(raw))
	for _, item := range raw {
		server, gameUserID, err := parseAdminGameBindingRef(adminGlobalGameBindingRef{
			Server:          item.Server,
			GameUserID:      item.GameUserID,
			GameUserIDSnake: item.GameUserIDSnake,
		})
		if err != nil {
			return nil, err
		}

		targetUserID := strings.TrimSpace(item.TargetUserID)
		if targetUserID == "" {
			targetUserID = strings.TrimSpace(item.TargetUserIDSnake)
		}
		if targetUserID == "" {
			return nil, fiber.NewError(fiber.StatusBadRequest, "targetUserId is required")
		}

		key := server + ":" + gameUserID
		if existing, ok := seen[key]; ok {
			if existing.targetUserID != targetUserID {
				return nil, fiber.NewError(fiber.StatusBadRequest, "duplicated binding with conflicting targetUserId")
			}
			continue
		}
		seen[key] = dedupeValue{
			server:       server,
			gameUserID:   gameUserID,
			targetUserID: targetUserID,
		}
		out = append(out, adminGlobalGameBindingBatchReassignItem{
			Server:       server,
			GameUserID:   gameUserID,
			TargetUserID: targetUserID,
		})
	}

	if len(out) == 0 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "items is required")
	}
	if len(out) > maxBatchGameBindingOperationCount {
		return nil, fiber.NewError(fiber.StatusBadRequest, "too many items in one batch")
	}
	return out, nil
}

func queryManageableTargetUserByID(ctx fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, actorUserID, actorRole, targetUserID string) (*postgresql.User, error) {
	userID := strings.TrimSpace(targetUserID)
	if userID == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "targetUserId is required")
	}

	targetUser, err := apiHelper.DBManager.DB.User.Query().
		Where(userSchema.IDEQ(userID)).
		Select(userSchema.FieldID, userSchema.FieldRole).
		Only(ctx.Context())
	if err != nil {
		if postgresql.IsNotFound(err) {
			return nil, fiber.NewError(fiber.StatusNotFound, "target user not found")
		}
		return nil, fiber.NewError(fiber.StatusInternalServerError, "failed to query target user")
	}

	if err := ensureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
		return nil, err
	}
	return targetUser, nil
}

func queryGameBindingWithOwner(ctx fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, server, gameUserID string) (*postgresql.GameAccountBinding, error) {
	row, err := apiHelper.DBManager.DB.GameAccountBinding.Query().
		Where(
			gameaccountbinding.ServerEQ(server),
			gameaccountbinding.GameUserIDEQ(gameUserID),
		).
		WithUser(func(query *postgresql.UserQuery) {
			query.Select(userSchema.FieldID, userSchema.FieldRole)
		}).
		Only(ctx.Context())
	if err != nil {
		return nil, err
	}
	return row, nil
}

func ensureAdminCanManageBindingOwner(actorUserID, actorRole string, row *postgresql.GameAccountBinding) error {
	if row == nil || row.Edges.User == nil {
		return fiber.NewError(fiber.StatusInternalServerError, "binding owner is missing")
	}
	return ensureAdminCanManageTargetUser(actorUserID, actorRole, row.Edges.User.ID, string(row.Edges.User.Role))
}

func handleAdminListGlobalGameAccountBindings(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		_, actorRole, err := currentAdminActor(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		filters, err := parseAdminGlobalGameBindingQueryFilters(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.list", "game_account", "all", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_query_filters", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid query filters")
		}

		dbCtx := c.Context()
		baseQuery := applyAdminGlobalGameBindingFilters(apiHelper.DBManager.DB.GameAccountBinding.Query(), filters, actorRole)

		total, err := baseQuery.Clone().Count(dbCtx)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.list", "game_account", "all", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("count_bindings_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to count game account bindings")
		}

		offset := (filters.Page - 1) * filters.PageSize
		rows, err := applyAdminGlobalGameBindingSort(baseQuery.Clone(), filters.Sort).
			Limit(filters.PageSize).
			Offset(offset).
			WithUser(func(query *postgresql.UserQuery) {
				query.Select(userSchema.FieldID, userSchema.FieldName, userSchema.FieldEmail, userSchema.FieldRole)
			}).
			All(dbCtx)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.list", "game_account", "all", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_bindings_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query game account bindings")
		}

		totalPages := 0
		if total > 0 {
			totalPages = int(math.Ceil(float64(total) / float64(filters.PageSize)))
		}
		resp := adminGlobalGameBindingListResponse{
			GeneratedAt: time.Now().UTC(),
			Page:        filters.Page,
			PageSize:    filters.PageSize,
			Total:       total,
			TotalPages:  totalPages,
			HasMore:     filters.Page*filters.PageSize < total,
			Sort:        filters.Sort,
			Filters: adminGlobalGameBindingAppliedFilters{
				Query:      filters.Query,
				Server:     filters.Server,
				GameUserID: filters.GameUserID,
				UserID:     filters.UserID,
				Verified:   filters.Verified,
			},
			Items: buildAdminGlobalGameBindingItems(rows),
		}

		writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.list", "game_account", "all", harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"total": total,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleAdminDeleteGlobalGameAccountBinding(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, actorRole, err := currentAdminActor(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		server, gameUserID, err := parseAdminGameBindingPath(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.delete", "game_account", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_path_params", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid path params")
		}

		row, err := queryGameBindingWithOwner(c, apiHelper, server, gameUserID)
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.delete", "game_account", server+":"+gameUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("binding_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "binding not found")
			}
			writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.delete", "game_account", server+":"+gameUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_binding_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query binding")
		}
		if err := ensureAdminCanManageBindingOwner(actorUserID, actorRole, row); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.delete", "game_account", server+":"+gameUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("permission_denied", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorForbidden(c, "insufficient permissions")
		}

		if err := apiHelper.DBManager.DB.GameAccountBinding.DeleteOneID(row.ID).Exec(c.Context()); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.delete", "game_account", server+":"+gameUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("delete_binding_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to delete binding")
		}

		writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.delete", "game_account", server+":"+gameUserID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"sourceUserID": row.Edges.User.ID,
		})
		return harukiAPIHelper.SuccessResponse[string](c, "binding deleted", nil)
	}
}

func handleAdminReassignGlobalGameAccountBinding(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, actorRole, err := currentAdminActor(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		server, gameUserID, err := parseAdminGameBindingPath(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.reassign", "game_account", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_path_params", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid path params")
		}

		targetUserID, err := parseAdminGameBindingReassignPayload(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.reassign", "game_account", server+":"+gameUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		targetUser, err := queryManageableTargetUserByID(c, apiHelper, actorUserID, actorRole, targetUserID)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.reassign", "game_account", server+":"+gameUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_target_user", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid target user")
		}

		row, err := queryGameBindingWithOwner(c, apiHelper, server, gameUserID)
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.reassign", "game_account", server+":"+gameUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("binding_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "binding not found")
			}
			writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.reassign", "game_account", server+":"+gameUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_binding_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query binding")
		}
		if err := ensureAdminCanManageBindingOwner(actorUserID, actorRole, row); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.reassign", "game_account", server+":"+gameUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("permission_denied", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorForbidden(c, "insufficient permissions")
		}

		resp := adminGlobalGameBindingReassignResponse{
			Server:       server,
			GameUserID:   gameUserID,
			FromUserID:   row.Edges.User.ID,
			TargetUserID: targetUser.ID,
			Changed:      row.Edges.User.ID != targetUser.ID,
		}
		if resp.Changed {
			if _, err := row.Update().SetUserID(targetUser.ID).Save(c.Context()); err != nil {
				writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.reassign", "game_account", server+":"+gameUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("reassign_binding_failed", nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to reassign binding")
			}
		}

		writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.reassign", "game_account", server+":"+gameUserID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"sourceUserID": row.Edges.User.ID,
			"targetUserID": targetUser.ID,
			"changed":      resp.Changed,
		})
		return harukiAPIHelper.SuccessResponse(c, "binding reassigned", &resp)
	}
}

func handleAdminBatchDeleteGlobalGameAccountBindings(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, actorRole, err := currentAdminActor(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		var payload adminGlobalGameBindingBatchDeletePayload
		if err := c.Bind().Body(&payload); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.batch_delete", "game_account", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}
		items, err := sanitizeAdminBatchGameBindingRefs(payload.Items)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.batch_delete", "game_account", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_items", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid items")
		}

		results := make([]adminGlobalGameBindingBatchDeleteItemResult, 0, len(items))
		successCount := 0
		for _, item := range items {
			result := adminGlobalGameBindingBatchDeleteItemResult{
				Server:     item.Server,
				GameUserID: item.GameUserID,
			}

			row, err := queryGameBindingWithOwner(c, apiHelper, item.Server, item.GameUserID)
			if err != nil {
				if postgresql.IsNotFound(err) {
					result.Code = "binding_not_found"
					result.Message = "binding not found"
					results = append(results, result)
					continue
				}
				result.Code = "query_binding_failed"
				result.Message = "failed to query binding"
				results = append(results, result)
				continue
			}

			if err := ensureAdminCanManageBindingOwner(actorUserID, actorRole, row); err != nil {
				result.Code = "permission_denied"
				result.Message = "insufficient permissions"
				results = append(results, result)
				continue
			}

			if err := apiHelper.DBManager.DB.GameAccountBinding.DeleteOneID(row.ID).Exec(c.Context()); err != nil {
				result.Code = "delete_binding_failed"
				result.Message = "failed to delete binding"
				results = append(results, result)
				continue
			}

			result.Success = true
			successCount++
			results = append(results, result)
		}

		resp := adminGlobalGameBindingBatchDeleteResponse{
			Total:   len(items),
			Success: successCount,
			Failed:  len(items) - successCount,
			Results: results,
		}
		resultState := harukiAPIHelper.SystemLogResultSuccess
		if resp.Failed > 0 {
			resultState = harukiAPIHelper.SystemLogResultFailure
		}
		writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.batch_delete", "game_account", "batch", resultState, map[string]any{
			"total":   resp.Total,
			"success": resp.Success,
			"failed":  resp.Failed,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleAdminBatchReassignGlobalGameAccountBindings(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		actorUserID, actorRole, err := currentAdminActor(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		var payload adminGlobalGameBindingBatchReassignPayload
		if err := c.Bind().Body(&payload); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.batch_reassign", "game_account", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}
		items, err := sanitizeAdminBatchGameBindingReassignItems(payload.Items)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.batch_reassign", "game_account", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_items", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid items")
		}

		targetUserCache := make(map[string]*postgresql.User, len(items))
		results := make([]adminGlobalGameBindingBatchReassignItemResult, 0, len(items))
		successCount := 0
		for _, item := range items {
			result := adminGlobalGameBindingBatchReassignItemResult{
				Server:       item.Server,
				GameUserID:   item.GameUserID,
				TargetUserID: item.TargetUserID,
			}

			row, err := queryGameBindingWithOwner(c, apiHelper, item.Server, item.GameUserID)
			if err != nil {
				if postgresql.IsNotFound(err) {
					result.Code = "binding_not_found"
					result.Message = "binding not found"
					results = append(results, result)
					continue
				}
				result.Code = "query_binding_failed"
				result.Message = "failed to query binding"
				results = append(results, result)
				continue
			}

			if err := ensureAdminCanManageBindingOwner(actorUserID, actorRole, row); err != nil {
				result.Code = "permission_denied"
				result.Message = "insufficient permissions"
				results = append(results, result)
				continue
			}

			targetUser, ok := targetUserCache[item.TargetUserID]
			if !ok {
				targetUser, err = queryManageableTargetUserByID(c, apiHelper, actorUserID, actorRole, item.TargetUserID)
				if err != nil {
					result.Code = "invalid_target_user"
					if fiberErr, ok := err.(*fiber.Error); ok {
						result.Message = fiberErr.Message
					} else {
						result.Message = "invalid target user"
					}
					results = append(results, result)
					continue
				}
				targetUserCache[item.TargetUserID] = targetUser
			}

			result.FromUserID = row.Edges.User.ID
			result.Changed = row.Edges.User.ID != targetUser.ID
			if result.Changed {
				if _, err := row.Update().SetUserID(targetUser.ID).Save(c.Context()); err != nil {
					result.Code = "reassign_binding_failed"
					result.Message = "failed to reassign binding"
					results = append(results, result)
					continue
				}
			}

			result.Success = true
			successCount++
			results = append(results, result)
		}

		resp := adminGlobalGameBindingBatchReassignResponse{
			Total:   len(items),
			Success: successCount,
			Failed:  len(items) - successCount,
			Results: results,
		}
		resultState := harukiAPIHelper.SystemLogResultSuccess
		if resp.Failed > 0 {
			resultState = harukiAPIHelper.SystemLogResultFailure
		}
		writeAdminAuditLog(c, apiHelper, "admin.game_account_binding.global.batch_reassign", "game_account", "batch", resultState, map[string]any{
			"total":   resp.Total,
			"success": resp.Success,
			"failed":  resp.Failed,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func registerAdminGlobalGameAccountBindingRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router) {
	gameBindings := adminGroup.Group("/game-account-bindings", RequireAdmin(apiHelper))
	gameBindings.Get("/", handleAdminListGlobalGameAccountBindings(apiHelper))
	gameBindings.Post("/batch-delete", handleAdminBatchDeleteGlobalGameAccountBindings(apiHelper))
	gameBindings.Post("/batch-reassign", handleAdminBatchReassignGlobalGameAccountBindings(apiHelper))
	gameBindings.Put("/:server/:game_user_id/reassign", handleAdminReassignGlobalGameAccountBinding(apiHelper))
	gameBindings.Delete("/:server/:game_user_id", handleAdminDeleteGlobalGameAccountBinding(apiHelper))
}

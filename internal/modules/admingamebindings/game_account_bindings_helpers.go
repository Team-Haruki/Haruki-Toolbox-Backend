package admingamebindings

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	platformPagination "haruki-suite/internal/platform/pagination"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/gameaccountbinding"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"strconv"
	"strings"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

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

	verified, err := adminCoreModule.ParseOptionalBoolField(c.Query("verified"), "verified")
	if err != nil {
		return nil, err
	}
	page, pageSize, err := platformPagination.ParsePageAndPageSize(c, defaultAdminGlobalGameBindingPage, defaultAdminGlobalGameBindingPageSize, maxAdminGlobalGameBindingPageSize)
	if err != nil {
		return nil, err
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

	if err := adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
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
	return adminCoreModule.EnsureAdminCanManageTargetUser(actorUserID, actorRole, row.Edges.User.ID, string(row.Edges.User.Role))
}

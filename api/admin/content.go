package admin

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/friendlink"
	"haruki-suite/utils/database/postgresql/group"
	"haruki-suite/utils/database/postgresql/grouplist"
	"strconv"
	"strings"
	"time"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

type adminFriendLinkPayload struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Avatar      string   `json:"avatar"`
	URL         string   `json:"url"`
	Tags        []string `json:"tags"`
}

type adminFriendLinkItem struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Avatar      string   `json:"avatar"`
	URL         string   `json:"url"`
	Tags        []string `json:"tags"`
}

type adminFriendLinksResponse struct {
	GeneratedAt time.Time             `json:"generatedAt"`
	Total       int                   `json:"total"`
	Items       []adminFriendLinkItem `json:"items"`
}

type adminFriendGroupPayload struct {
	Group string `json:"group"`
}

type adminFriendGroupItemPayload struct {
	Name      string  `json:"name"`
	Avatar    *string `json:"avatar"`
	Bg        *string `json:"bg"`
	GroupInfo string  `json:"groupInfo"`
	Detail    string  `json:"detail"`
}

type adminFriendGroupItem struct {
	ID        int     `json:"id"`
	Name      string  `json:"name"`
	Avatar    *string `json:"avatar,omitempty"`
	Bg        *string `json:"bg,omitempty"`
	GroupInfo string  `json:"groupInfo"`
	Detail    string  `json:"detail"`
}

type adminFriendGroup struct {
	ID        int                    `json:"id"`
	Group     string                 `json:"group"`
	GroupList []adminFriendGroupItem `json:"groupList"`
}

type adminFriendGroupsResponse struct {
	GeneratedAt time.Time          `json:"generatedAt"`
	TotalGroups int                `json:"totalGroups"`
	TotalItems  int                `json:"totalItems"`
	Items       []adminFriendGroup `json:"items"`
}

type adminFriendGroupCreateResponse struct {
	ID    int    `json:"id"`
	Group string `json:"group"`
}

type adminFriendGroupItemResponse struct {
	GroupID int                  `json:"groupId"`
	Created bool                 `json:"created"`
	Item    adminFriendGroupItem `json:"item"`
}

func parseAdminPathPositiveInt(raw string, name string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 0, fiber.NewError(fiber.StatusBadRequest, name+" must be a positive integer")
	}
	return value, nil
}

func sanitizeAdminTags(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func parseAdminFriendLinkPayload(c fiber.Ctx) (*adminFriendLinkPayload, error) {
	var payload adminFriendLinkPayload
	if err := c.Bind().Body(&payload); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	payload.Name = strings.TrimSpace(payload.Name)
	payload.Description = strings.TrimSpace(payload.Description)
	payload.Avatar = strings.TrimSpace(payload.Avatar)
	payload.URL = strings.TrimSpace(payload.URL)
	payload.Tags = sanitizeAdminTags(payload.Tags)

	if payload.Name == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "name is required")
	}
	if payload.Description == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "description is required")
	}
	if payload.URL == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "url is required")
	}
	if len(payload.Name) > 100 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "name exceeds max length")
	}
	if len(payload.Description) > 300 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "description exceeds max length")
	}
	if len(payload.Avatar) > 500 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "avatar exceeds max length")
	}
	if len(payload.URL) > 500 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "url exceeds max length")
	}
	return &payload, nil
}

func parseAdminFriendGroupPayload(c fiber.Ctx) (*adminFriendGroupPayload, error) {
	var payload adminFriendGroupPayload
	if err := c.Bind().Body(&payload); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	payload.Group = strings.TrimSpace(payload.Group)
	if payload.Group == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "group is required")
	}
	if len(payload.Group) > 64 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "group exceeds max length")
	}
	return &payload, nil
}

func normalizeAdminOptionalString(raw *string, maxLen int, fieldName string) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*raw)
	if len(trimmed) > maxLen {
		return nil, fiber.NewError(fiber.StatusBadRequest, fieldName+" exceeds max length")
	}
	out := trimmed
	return &out, nil
}

func parseAdminFriendGroupItemPayload(c fiber.Ctx) (*adminFriendGroupItemPayload, error) {
	var payload adminFriendGroupItemPayload
	if err := c.Bind().Body(&payload); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	payload.Name = strings.TrimSpace(payload.Name)
	payload.GroupInfo = strings.TrimSpace(payload.GroupInfo)
	payload.Detail = strings.TrimSpace(payload.Detail)
	var err error
	payload.Avatar, err = normalizeAdminOptionalString(payload.Avatar, 500, "avatar")
	if err != nil {
		return nil, err
	}
	payload.Bg, err = normalizeAdminOptionalString(payload.Bg, 500, "bg")
	if err != nil {
		return nil, err
	}

	if payload.Name == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "name is required")
	}
	if payload.GroupInfo == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "groupInfo is required")
	}
	if payload.Detail == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "detail is required")
	}
	if len(payload.Name) > 64 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "name exceeds max length")
	}
	if len(payload.GroupInfo) > 100 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "groupInfo exceeds max length")
	}
	if len(payload.Detail) > 300 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "detail exceeds max length")
	}
	return &payload, nil
}

func buildAdminFriendLinkItem(row *postgresql.FriendLink) adminFriendLinkItem {
	return adminFriendLinkItem{
		ID:          row.ID,
		Name:        row.Name,
		Description: row.Description,
		Avatar:      row.Avatar,
		URL:         row.URL,
		Tags:        append([]string(nil), row.Tags...),
	}
}

func buildAdminFriendGroupItems(rows []*postgresql.GroupList) []adminFriendGroupItem {
	items := make([]adminFriendGroupItem, 0, len(rows))
	for _, row := range rows {
		item := adminFriendGroupItem{
			ID:        row.ID,
			Name:      row.Name,
			GroupInfo: row.GroupInfo,
			Detail:    row.Detail,
		}
		if row.Avatar != nil {
			avatar := *row.Avatar
			item.Avatar = &avatar
		}
		if row.Bg != nil {
			bg := *row.Bg
			item.Bg = &bg
		}
		items = append(items, item)
	}
	return items
}

func handleAdminListFriendLinks(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = "admin.content.friend_link.list"
		rows, err := apiHelper.DBManager.DB.FriendLink.Query().
			Order(friendlink.ByID(sql.OrderAsc())).
			All(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "friend_link", "all", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_friend_links_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query friend links")
		}

		items := make([]adminFriendLinkItem, 0, len(rows))
		for _, row := range rows {
			items = append(items, buildAdminFriendLinkItem(row))
		}

		resp := adminFriendLinksResponse{
			GeneratedAt: time.Now().UTC(),
			Total:       len(items),
			Items:       items,
		}
		writeAdminAuditLog(c, apiHelper, action, "friend_link", "all", harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"total": resp.Total,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleAdminCreateFriendLink(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = "admin.content.friend_link.create"
		payload, err := parseAdminFriendLinkPayload(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "friend_link", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		created, err := apiHelper.DBManager.DB.FriendLink.Create().
			SetName(payload.Name).
			SetDescription(payload.Description).
			SetAvatar(payload.Avatar).
			SetURL(payload.URL).
			SetTags(payload.Tags).
			Save(c.Context())
		if err != nil {
			if postgresql.IsConstraintError(err) {
				writeAdminAuditLog(c, apiHelper, action, "friend_link", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("friend_link_conflict", nil))
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "friend link conflict", nil)
			}
			writeAdminAuditLog(c, apiHelper, action, "friend_link", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("create_friend_link_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to create friend link")
		}

		resp := buildAdminFriendLinkItem(created)
		writeAdminAuditLog(c, apiHelper, action, "friend_link", strconv.Itoa(created.ID), harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse(c, "friend link created", &resp)
	}
}

func handleAdminUpdateFriendLink(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = "admin.content.friend_link.update"
		friendLinkID, err := parseAdminPathPositiveInt(c.Params("id"), "id")
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "friend_link", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_friend_link_id", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid id")
		}

		payload, err := parseAdminFriendLinkPayload(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "friend_link", strconv.Itoa(friendLinkID), harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		updated, err := apiHelper.DBManager.DB.FriendLink.UpdateOneID(friendLinkID).
			SetName(payload.Name).
			SetDescription(payload.Description).
			SetAvatar(payload.Avatar).
			SetURL(payload.URL).
			SetTags(payload.Tags).
			Save(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, action, "friend_link", strconv.Itoa(friendLinkID), harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("friend_link_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "friend link not found")
			}
			if postgresql.IsConstraintError(err) {
				writeAdminAuditLog(c, apiHelper, action, "friend_link", strconv.Itoa(friendLinkID), harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("friend_link_conflict", nil))
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "friend link conflict", nil)
			}
			writeAdminAuditLog(c, apiHelper, action, "friend_link", strconv.Itoa(friendLinkID), harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("update_friend_link_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to update friend link")
		}

		resp := buildAdminFriendLinkItem(updated)
		writeAdminAuditLog(c, apiHelper, action, "friend_link", strconv.Itoa(friendLinkID), harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse(c, "friend link updated", &resp)
	}
}

func handleAdminDeleteFriendLink(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = "admin.content.friend_link.delete"
		friendLinkID, err := parseAdminPathPositiveInt(c.Params("id"), "id")
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "friend_link", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_friend_link_id", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid id")
		}

		err = apiHelper.DBManager.DB.FriendLink.DeleteOneID(friendLinkID).Exec(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, action, "friend_link", strconv.Itoa(friendLinkID), harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("friend_link_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "friend link not found")
			}
			writeAdminAuditLog(c, apiHelper, action, "friend_link", strconv.Itoa(friendLinkID), harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("delete_friend_link_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to delete friend link")
		}

		writeAdminAuditLog(c, apiHelper, action, "friend_link", strconv.Itoa(friendLinkID), harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse[string](c, "friend link deleted", nil)
	}
}

func handleAdminListFriendGroups(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = "admin.content.friend_group.list"
		rows, err := apiHelper.DBManager.DB.Group.Query().
			WithGroupList().
			Order(group.ByID(sql.OrderAsc())).
			All(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "friend_group", "all", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_friend_groups_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query friend groups")
		}

		items := make([]adminFriendGroup, 0, len(rows))
		totalItems := 0
		for _, row := range rows {
			groupItems := buildAdminFriendGroupItems(row.Edges.GroupList)
			totalItems += len(groupItems)
			items = append(items, adminFriendGroup{
				ID:        row.ID,
				Group:     row.Group,
				GroupList: groupItems,
			})
		}

		resp := adminFriendGroupsResponse{
			GeneratedAt: time.Now().UTC(),
			TotalGroups: len(items),
			TotalItems:  totalItems,
			Items:       items,
		}
		writeAdminAuditLog(c, apiHelper, action, "friend_group", "all", harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"groups": resp.TotalGroups,
			"items":  resp.TotalItems,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleAdminCreateFriendGroup(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = "admin.content.friend_group.create"
		payload, err := parseAdminFriendGroupPayload(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "friend_group", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		created, err := apiHelper.DBManager.DB.Group.Create().
			SetGroup(payload.Group).
			Save(c.Context())
		if err != nil {
			if postgresql.IsConstraintError(err) {
				writeAdminAuditLog(c, apiHelper, action, "friend_group", payload.Group, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("friend_group_conflict", nil))
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "friend group already exists", nil)
			}
			writeAdminAuditLog(c, apiHelper, action, "friend_group", payload.Group, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("create_friend_group_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to create friend group")
		}

		resp := adminFriendGroupCreateResponse{ID: created.ID, Group: created.Group}
		writeAdminAuditLog(c, apiHelper, action, "friend_group", strconv.Itoa(created.ID), harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse(c, "friend group created", &resp)
	}
}

func handleAdminDeleteFriendGroup(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = "admin.content.friend_group.delete"
		groupID, err := parseAdminPathPositiveInt(c.Params("group_id"), "group_id")
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "friend_group", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_group_id", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid group_id")
		}

		tx, err := apiHelper.DBManager.DB.Tx(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "friend_group", strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("start_transaction_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to start transaction")
		}
		if _, err := tx.GroupList.Delete().Where(grouplist.HasGroupWith(group.IDEQ(groupID))).Exec(c.Context()); err != nil {
			_ = tx.Rollback()
			writeAdminAuditLog(c, apiHelper, action, "friend_group", strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("delete_group_items_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to delete group items")
		}
		if err := tx.Group.DeleteOneID(groupID).Exec(c.Context()); err != nil {
			_ = tx.Rollback()
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, action, "friend_group", strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("friend_group_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "friend group not found")
			}
			writeAdminAuditLog(c, apiHelper, action, "friend_group", strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("delete_friend_group_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to delete friend group")
		}
		if err := tx.Commit(); err != nil {
			writeAdminAuditLog(c, apiHelper, action, "friend_group", strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("commit_transaction_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to commit friend group deletion")
		}

		writeAdminAuditLog(c, apiHelper, action, "friend_group", strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse[string](c, "friend group deleted", nil)
	}
}

func handleAdminCreateFriendGroupItem(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = "admin.content.friend_group_item.create"
		groupID, err := parseAdminPathPositiveInt(c.Params("group_id"), "group_id")
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "friend_group_item", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_group_id", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid group_id")
		}

		payload, err := parseAdminFriendGroupItemPayload(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "friend_group_item", strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		exists, err := apiHelper.DBManager.DB.Group.Query().Where(group.IDEQ(groupID)).Exist(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "friend_group_item", strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_group_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query friend group")
		}
		if !exists {
			writeAdminAuditLog(c, apiHelper, action, "friend_group_item", strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("friend_group_not_found", nil))
			return harukiAPIHelper.ErrorNotFound(c, "friend group not found")
		}

		builder := apiHelper.DBManager.DB.GroupList.Create().
			SetGroupID(groupID).
			SetName(payload.Name).
			SetGroupInfo(payload.GroupInfo).
			SetDetail(payload.Detail)
		if payload.Avatar != nil && *payload.Avatar != "" {
			builder.SetAvatar(*payload.Avatar)
		}
		if payload.Bg != nil && *payload.Bg != "" {
			builder.SetBg(*payload.Bg)
		}
		created, err := builder.Save(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "friend_group_item", strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("create_friend_group_item_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to create friend group item")
		}

		item := buildAdminFriendGroupItems([]*postgresql.GroupList{created})[0]
		resp := adminFriendGroupItemResponse{
			GroupID: groupID,
			Created: true,
			Item:    item,
		}
		writeAdminAuditLog(c, apiHelper, action, "friend_group_item", strconv.Itoa(created.ID), harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"groupID": groupID,
		})
		return harukiAPIHelper.SuccessResponse(c, "friend group item created", &resp)
	}
}

func handleAdminUpdateFriendGroupItem(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = "admin.content.friend_group_item.update"
		groupID, err := parseAdminPathPositiveInt(c.Params("group_id"), "group_id")
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "friend_group_item", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_group_id", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid group_id")
		}
		itemID, err := parseAdminPathPositiveInt(c.Params("item_id"), "item_id")
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "friend_group_item", strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_item_id", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid item_id")
		}

		payload, err := parseAdminFriendGroupItemPayload(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "friend_group_item", strconv.Itoa(itemID), harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		existing, err := apiHelper.DBManager.DB.GroupList.Query().
			Where(
				grouplist.IDEQ(itemID),
				grouplist.HasGroupWith(group.IDEQ(groupID)),
			).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, action, "friend_group_item", strconv.Itoa(itemID), harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("friend_group_item_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "friend group item not found")
			}
			writeAdminAuditLog(c, apiHelper, action, "friend_group_item", strconv.Itoa(itemID), harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_friend_group_item_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query friend group item")
		}

		updater := existing.Update().
			SetName(payload.Name).
			SetGroupInfo(payload.GroupInfo).
			SetDetail(payload.Detail)
		if payload.Avatar != nil {
			if *payload.Avatar == "" {
				updater.ClearAvatar()
			} else {
				updater.SetAvatar(*payload.Avatar)
			}
		}
		if payload.Bg != nil {
			if *payload.Bg == "" {
				updater.ClearBg()
			} else {
				updater.SetBg(*payload.Bg)
			}
		}
		updated, err := updater.Save(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "friend_group_item", strconv.Itoa(itemID), harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("update_friend_group_item_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to update friend group item")
		}

		item := buildAdminFriendGroupItems([]*postgresql.GroupList{updated})[0]
		resp := adminFriendGroupItemResponse{
			GroupID: groupID,
			Created: false,
			Item:    item,
		}
		writeAdminAuditLog(c, apiHelper, action, "friend_group_item", strconv.Itoa(itemID), harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"groupID": groupID,
		})
		return harukiAPIHelper.SuccessResponse(c, "friend group item updated", &resp)
	}
}

func handleAdminDeleteFriendGroupItem(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		const action = "admin.content.friend_group_item.delete"
		groupID, err := parseAdminPathPositiveInt(c.Params("group_id"), "group_id")
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "friend_group_item", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_group_id", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid group_id")
		}
		itemID, err := parseAdminPathPositiveInt(c.Params("item_id"), "item_id")
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "friend_group_item", strconv.Itoa(groupID), harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_item_id", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid item_id")
		}

		affected, err := apiHelper.DBManager.DB.GroupList.Delete().
			Where(
				grouplist.IDEQ(itemID),
				grouplist.HasGroupWith(group.IDEQ(groupID)),
			).
			Exec(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, action, "friend_group_item", strconv.Itoa(itemID), harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("delete_friend_group_item_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to delete friend group item")
		}
		if affected == 0 {
			writeAdminAuditLog(c, apiHelper, action, "friend_group_item", strconv.Itoa(itemID), harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("friend_group_item_not_found", nil))
			return harukiAPIHelper.ErrorNotFound(c, "friend group item not found")
		}

		writeAdminAuditLog(c, apiHelper, action, "friend_group_item", strconv.Itoa(itemID), harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"groupID": groupID,
		})
		return harukiAPIHelper.SuccessResponse[string](c, "friend group item deleted", nil)
	}
}

func registerAdminContentRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, adminGroup fiber.Router) {
	content := adminGroup.Group("/content", RequireAdmin(apiHelper))

	friendLinks := content.Group("/friend-links")
	friendLinks.Get("/", handleAdminListFriendLinks(apiHelper))
	friendLinks.Post("/", handleAdminCreateFriendLink(apiHelper))
	friendLinks.Put("/:id", handleAdminUpdateFriendLink(apiHelper))
	friendLinks.Delete("/:id", handleAdminDeleteFriendLink(apiHelper))

	friendGroups := content.Group("/friend-groups")
	friendGroups.Get("/", handleAdminListFriendGroups(apiHelper))
	friendGroups.Post("/", handleAdminCreateFriendGroup(apiHelper))
	friendGroups.Delete("/:group_id", handleAdminDeleteFriendGroup(apiHelper))
	friendGroups.Post("/:group_id/items", handleAdminCreateFriendGroupItem(apiHelper))
	friendGroups.Put("/:group_id/items/:item_id", handleAdminUpdateFriendGroupItem(apiHelper))
	friendGroups.Delete("/:group_id/items/:item_id", handleAdminDeleteFriendGroupItem(apiHelper))
}

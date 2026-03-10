package admincontent

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/friendlink"
	"haruki-suite/utils/database/postgresql/grouplist"
	"strconv"
	"strings"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

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

	groupName := strings.TrimSpace(payload.Group)
	if groupName == "" {
		groupName = strings.TrimSpace(payload.GroupName)
	}
	if groupName == "" {
		groupName = strings.TrimSpace(payload.GroupNameSnake)
	}
	if groupName == "" {
		groupName = strings.TrimSpace(payload.Name)
	}

	payload.Group = groupName
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
	groupInfo := strings.TrimSpace(payload.GroupInfo)
	if groupInfo == "" {
		groupInfo = strings.TrimSpace(payload.GroupInfoSnake)
	}
	detail := strings.TrimSpace(payload.Detail)
	if detail == "" {
		detail = strings.TrimSpace(payload.Description)
	}
	payload.GroupInfo = groupInfo
	payload.Detail = detail
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

func buildAdminFriendLinkCreateBuilder(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, payload *adminFriendLinkPayload) *postgresql.FriendLinkCreate {
	return apiHelper.DBManager.DB.FriendLink.Create().
		SetName(payload.Name).
		SetDescription(payload.Description).
		SetAvatar(payload.Avatar).
		SetURL(payload.URL).
		SetTags(payload.Tags)
}

func queryNextFriendLinkID(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) (int, error) {
	row, err := apiHelper.DBManager.DB.FriendLink.Query().
		Order(friendlink.ByID(sql.OrderDesc())).
		Select(friendlink.FieldID).
		First(c.Context())
	if err != nil {
		if postgresql.IsNotFound(err) {
			return 1, nil
		}
		return 0, err
	}
	return row.ID + 1, nil
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

func buildAdminFriendGroupItemCreateBuilder(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, groupID int, payload *adminFriendGroupItemPayload) *postgresql.GroupListCreate {
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
	return builder
}

func queryNextFriendGroupItemID(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) (int, error) {
	row, err := apiHelper.DBManager.DB.GroupList.Query().
		Order(grouplist.ByID(sql.OrderDesc())).
		Select(grouplist.FieldID).
		First(c.Context())
	if err != nil {
		if postgresql.IsNotFound(err) {
			return 1, nil
		}
		return 0, err
	}
	return row.ID + 1, nil
}

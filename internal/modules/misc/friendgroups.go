package misc

import (
	"fmt"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/group"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/grouplist"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

func handleGetFriendGroups(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		groups, err := apiHelper.DBManager.DB.Group.Query().
			WithGroupList(func(q *postgresql.GroupListQuery) {
				q.Order(
					grouplist.BySortOrder(sql.OrderAsc()),
					grouplist.ByID(sql.OrderAsc()),
				)
			}).
			Order(
				group.BySortOrder(sql.OrderAsc()),
				group.ByID(sql.OrderAsc()),
			).
			All(ctx)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "Failed to fetch friend groups")
		}
		var result []FriendGroupData
		for _, g := range groups {
			var items []FriendGroupItem
			for _, item := range g.Edges.GroupList {
				var avatarPath string
				var bgPath string
				if item.Avatar != nil {
					avatarPath = fmt.Sprintf("%s/friend-links/%s", config.Cfg.UserSystem.AvatarURL, *item.Avatar)
				}
				if item.Bg != nil {
					bgPath = fmt.Sprintf("%s/friend-links/%s", config.Cfg.UserSystem.AvatarURL, *item.Bg)
				}
				items = append(items, FriendGroupItem{
					Name:      item.Name,
					Avatar:    &avatarPath,
					Bg:        &bgPath,
					GroupInfo: item.GroupInfo,
					Detail:    item.Detail,
				})
			}
			result = append(result, FriendGroupData{
				Group:     g.Group,
				GroupList: items,
			})
		}
		return harukiAPIHelper.SuccessResponse[[]FriendGroupData](c, "Successfully fetched friend groups", &result)
	}
}

func registerFriendGroupsRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	api := apiHelper.Router.Group("/api/misc")
	api.Get("/friend_groups", handleGetFriendGroups(apiHelper))
}

package misc

import (
	"fmt"
	"haruki-suite/config"
	harukiAPIHelper "haruki-suite/utils/api"

	"github.com/gofiber/fiber/v3"
)

func handleGetFriendGroups(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		groups, err := apiHelper.DBManager.DB.Group.Query().
			WithGroupList().
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
	api := apiHelper.Router.Group("/misc")
	api.Get("/friend_groups", handleGetFriendGroups(apiHelper))
}

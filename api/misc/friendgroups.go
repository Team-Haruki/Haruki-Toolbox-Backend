package misc

import (
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
				items = append(items, FriendGroupItem{
					Name:      item.Name,
					Avatar:    item.Avatar,
					Bg:        item.Bg,
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

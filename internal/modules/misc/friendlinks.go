package misc

import (
	"fmt"
	"haruki-suite/config"
	harukiAPIHelper "haruki-suite/utils/api"

	"github.com/gofiber/fiber/v3"
)

type FriendLinkData struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Avatar      string   `json:"avatar"`
	URL         string   `json:"url"`
	Tags        []string `json:"tags"`
}

func handleGetFriendLinks(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		links, err := apiHelper.DBManager.DB.FriendLink.Query().All(ctx)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "Failed to fetch friend links")
		}
		var result []FriendLinkData
		for _, link := range links {
			tags := link.Tags
			if tags == nil {
				tags = []string{}
			}
			var avatarPath string
			if link.Avatar != "" {
				avatarPath = fmt.Sprintf("%s/friend-links/%s", config.Cfg.UserSystem.AvatarURL, link.Avatar)
			}
			result = append(result, FriendLinkData{
				ID:          link.ID,
				Name:        link.Name,
				Description: link.Description,
				Avatar:      avatarPath,
				URL:         link.URL,
				Tags:        tags,
			})
		}
		return harukiAPIHelper.SuccessResponse[[]FriendLinkData](c, "Successfully fetched friend links", &result)
	}
}

func registerFriendLinksRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	api := apiHelper.Router.Group("/api/misc")
	api.Get("/friend_links", handleGetFriendLinks(apiHelper))
}

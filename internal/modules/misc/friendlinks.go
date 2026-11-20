package misc

import (
	"strconv"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/pagination"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"

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

func handleGetFriendLinks(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, assets AssetsConfig) fiber.Handler {
	return func(c fiber.Ctx) error {
		page, size, err := pagination.ParsePageAndPageSize(c, 1, postgresql.DefaultFriendLinkPageSize, postgresql.MaxFriendLinkPageSize)
		if err != nil {
			return err
		}
		links, hasMore, err := apiHelper.DBManager.DB.ReadFriendLinkPage(c.Context(), page, size)
		if err != nil {
			return harukiAPIHelper.ErrorInternal(c, "Failed to fetch friend links")
		}
		if hasMore {
			c.Set("X-Next-Page", strconv.Itoa(page+1))
			c.Append(fiber.HeaderAccessControlExposeHeaders, "X-Next-Page")
		}
		var result []FriendLinkData
		for _, link := range links {
			tags := link.Tags
			if tags == nil {
				tags = []string{}
			}
			var avatarPath string
			if link.Avatar != "" {
				avatarPath = assets.FriendLinkURL(link.Avatar)
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
		return harukiAPIHelper.Responses.SuccessResponse[[]FriendLinkData](c, "Successfully fetched friend links", &result)
	}
}

func registerFriendLinksRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, assets AssetsConfig) {
	api := apiHelper.Router.Group("/api/misc")
	api.Get("/friend_links", handleGetFriendLinks(apiHelper, assets))
}

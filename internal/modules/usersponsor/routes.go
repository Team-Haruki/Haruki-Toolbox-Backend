package usersponsor

import (
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/sponsor"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/userprivateapi"
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(helper *api.HarukiToolboxRouterHelpers, cfg OAuthConfig, orders sponsor.AfdianConfig, catalogs ...*Catalog) {
	if helper == nil || helper.Router == nil || helper.SessionHandler == nil {
		return
	}
	var catalog *Catalog
	if len(catalogs) > 0 {
		catalog = catalogs[0]
	}
	if catalog != nil {
		helper.Router.Get(previewRoute+":filename", catalog.preview)
	}
	h := &handlers{catalog: catalog, api: helper, oauth: cfg, orders: orders, client: newOAuthHTTPClient()}
	r := helper.Router.Group("/api/user/:toolbox_user_id")
	guards := usercore.RequireAuthenticatedSelf(helper, "toolbox_user_id")
	// Fiber group middleware matches a prefix, so putting these guards on the
	// group also treats /api/user/me as toolbox_user_id="me". Scope them to
	// the sponsor endpoints so current-user bootstrap and sibling routes work.
	register := func(method, path string, handlers ...fiber.Handler) {
		first, rest := usercore.RouteHandlerParts(guards, handlers...)
		r.Add([]string{method}, path, first, rest...)
	}
	register(fiber.MethodGet, "/afdian", h.status)
	register(fiber.MethodGet, "/sponsor-frames", h.frames)
	register(fiber.MethodPost, "/afdian/authorize", h.authorize)
	register(fiber.MethodPost, "/afdian/callback", h.callback)
	register(fiber.MethodPost, "/afdian/verify-order", usercore.RequireVerifiedEmail(), h.verifyOrder)
	register(fiber.MethodDelete, "/afdian", h.unlink)
	register(fiber.MethodPut, "/sponsor-frames/:server/:game_user_id", h.saveFrame)
	helper.Router.Get("/api/private/game-frame/:server/:game_user_id", userprivateapi.ValidateUserPermission(helper), h.gameFrame)
}

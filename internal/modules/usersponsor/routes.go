package usersponsor

import (
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/userprivateapi"
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
)

func RegisterRoutes(helper *api.HarukiToolboxRouterHelpers, cfg OAuthConfig) {
	if helper == nil || helper.Router == nil || helper.SessionHandler == nil {
		return
	}
	h := &handlers{api: helper, oauth: cfg, client: newOAuthHTTPClient()}
	r := helper.Router.Group("/api/user/:toolbox_user_id", usercore.RouteHandlers(usercore.RequireAuthenticatedSelf(helper, "toolbox_user_id"))...)
	r.Get("/afdian", h.status)
	r.Get("/sponsor-frames", h.frames)
	r.Post("/afdian/authorize", h.authorize)
	r.Post("/afdian/callback", h.callback)
	r.Delete("/afdian", h.unlink)
	r.Put("/sponsor-frames/:server/:game_user_id", h.saveFrame)
	helper.Router.Get("/api/private/game-frame/:server/:game_user_id", userprivateapi.ValidateUserPermission(helper), h.gameFrame)
}

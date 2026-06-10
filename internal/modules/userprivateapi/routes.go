package userprivateapi

import harukiApiHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"

func RegisterUserPrivateAPIRoutes(apiHelper *harukiApiHelper.HarukiToolboxRouterHelpers) {
	privateAPI := apiHelper.Router.Group("/api/private", ValidateUserPermission(apiHelper))

	privateAPI.Get("/game-data/:server/:data_type/:user_id", handleGetPrivateData(apiHelper))
	privateAPI.Get("/game-binding", handleGetGameBindings(apiHelper))
}

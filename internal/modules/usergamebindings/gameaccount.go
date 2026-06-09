package usergamebindings

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiAPIHelper "haruki-suite/utils/api"
)

func RegisterUserGameAccountBindingRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id/game-account", userCoreModule.RouteHandlers(userCoreModule.RequireAuthenticatedSelf(apiHelper, "toolbox_user_id"))...)

	r.Get(
		"/:server/:game_user_id/recommend-data",
		handleGetDeckRecommendData(apiHelper),
	)
	r.Get(
		"/:server/:game_user_id/:data_type",
		handleGetOwnedGameAccountData(apiHelper),
	)

	r.RouteChain("/:server/:game_user_id").
		Post(
			handleGenerateGameAccountVerificationCode(apiHelper),
		).
		Put(
			handleCreateGameAccountBinding(apiHelper),
		).
		Patch(
			handleUpdateGameAccountBinding(apiHelper),
		).
		Delete(
			handleDeleteGameAccountBinding(apiHelper),
		)
}

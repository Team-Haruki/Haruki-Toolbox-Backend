package usergamebindings

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiAPIHelper "haruki-suite/utils/api"
)

func RegisterUserGameAccountBindingRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id/game-account", userCoreModule.RouteHandlers(userCoreModule.RequireAuthenticatedSelf(apiHelper, "toolbox_user_id"))...)
	grants := apiHelper.Router.Group("/api/user/:toolbox_user_id/game-account-grants", userCoreModule.RouteHandlers(userCoreModule.RequireAuthenticatedVerifiedSelf(apiHelper, "toolbox_user_id"))...)

	grants.Get("", handleListOwnedGameAccountDataGrants(apiHelper))
	grants.Get("/received", handleListReceivedGameAccountDataGrants(apiHelper))
	grants.RouteChain("/:server/:game_user_id/:data_type/:grantee_user_id").
		Put(handleUpsertGameAccountDataGrant(apiHelper)).
		Delete(handleDeleteGameAccountDataGrant(apiHelper))

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

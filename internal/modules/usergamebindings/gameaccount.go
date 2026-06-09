package usergamebindings

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiAPIHelper "haruki-suite/utils/api"
)

func RegisterUserGameAccountBindingRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id/game-account")

	verifySession := apiHelper.SessionHandler.VerifySessionToken
	requireSelf := userCoreModule.RequireSelfUserParam("toolbox_user_id")
	checkNotBanned := userCoreModule.CheckUserNotBanned(apiHelper)

	r.Get(
		"/:server/:game_user_id/recommend-data",
		verifySession,
		requireSelf,
		checkNotBanned,
		handleGetDeckRecommendData(apiHelper),
	)
	r.Get(
		"/:server/:game_user_id/:data_type",
		verifySession,
		requireSelf,
		checkNotBanned,
		handleGetOwnedGameAccountData(apiHelper),
	)

	r.RouteChain("/:server/:game_user_id").
		Post(
			verifySession,
			requireSelf,
			checkNotBanned,
			handleGenerateGameAccountVerificationCode(apiHelper),
		).
		Put(
			verifySession,
			requireSelf,
			checkNotBanned,
			handleCreateGameAccountBinding(apiHelper),
		).
		Patch(
			verifySession,
			requireSelf,
			checkNotBanned,
			handleUpdateGameAccountBinding(apiHelper),
		).
		Delete(
			verifySession,
			requireSelf,
			checkNotBanned,
			handleDeleteGameAccountBinding(apiHelper),
		)
}

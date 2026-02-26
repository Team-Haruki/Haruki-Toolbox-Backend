package oauth2

import (
	harukiAPIHelper "haruki-suite/utils/api"
)

func RegisterOAuth2Routes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {

	apiHelper.Router.Get("/api/oauth2/authorize", handleAuthorize(apiHelper))
	apiHelper.Router.Post("/api/oauth2/token", handleToken(apiHelper))
	apiHelper.Router.Post("/api/oauth2/revoke", handleRevoke(apiHelper))

	apiHelper.Router.Post("/api/oauth2/authorize/consent", apiHelper.SessionHandler.VerifySessionToken, handleConsent(apiHelper))

	registerOAuth2UserInfoRoutes(apiHelper)

	registerOAuth2GameDataRoutes(apiHelper)
}

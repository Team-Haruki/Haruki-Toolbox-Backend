package oauth2

import (
	harukiAPIHelper "haruki-suite/utils/api"
)

func RegisterOAuth2Routes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	// Public OAuth2 endpoints (no auth required)
	apiHelper.Router.Get("/api/oauth2/authorize", handleAuthorize(apiHelper))
	apiHelper.Router.Post("/api/oauth2/token", handleToken(apiHelper))
	apiHelper.Router.Post("/api/oauth2/revoke", handleRevoke(apiHelper))

	// Consent endpoint (requires user session)
	apiHelper.Router.Post("/api/oauth2/authorize/consent", apiHelper.SessionHandler.VerifySessionToken, handleConsent(apiHelper))

	// OAuth2-protected user info endpoints
	registerOAuth2UserInfoRoutes(apiHelper)

	// OAuth2-protected game data endpoint
	registerOAuth2GameDataRoutes(apiHelper)
}

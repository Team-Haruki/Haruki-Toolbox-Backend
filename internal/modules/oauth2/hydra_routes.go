package oauth2

import (
	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiAPIHelper "haruki-suite/utils/api"
)

func registerHydraOAuth2Routes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	apiHelper.Router.Get("/api/oauth2/authorize", handleHydraAuthorizeRedirect())
	apiHelper.Router.Post("/api/oauth2/token", handleHydraPublicProxy("/oauth2/token"))
	apiHelper.Router.Post("/api/oauth2/revoke", handleHydraPublicProxy("/oauth2/revoke"))

	apiHelper.Router.Get("/api/oauth2/login", handleHydraGetLoginRequest())
	apiHelper.Router.Post("/api/oauth2/login/accept", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.CheckUserNotBanned(apiHelper), handleHydraAcceptLogin())
	apiHelper.Router.Post("/api/oauth2/login/reject", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.CheckUserNotBanned(apiHelper), handleHydraRejectLogin())

	apiHelper.Router.Get("/api/oauth2/consent", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.CheckUserNotBanned(apiHelper), handleHydraGetConsentRequest())
	apiHelper.Router.Post("/api/oauth2/consent/accept", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.CheckUserNotBanned(apiHelper), handleHydraAcceptConsent(apiHelper))
	apiHelper.Router.Post("/api/oauth2/consent/reject", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.CheckUserNotBanned(apiHelper), handleHydraRejectConsent())

	// Legacy frontend compatibility.
	apiHelper.Router.Post("/api/oauth2/authorize/consent", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.CheckUserNotBanned(apiHelper), handleHydraLegacyConsentDecision(apiHelper))
}

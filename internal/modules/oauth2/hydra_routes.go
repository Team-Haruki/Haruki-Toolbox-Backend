package oauth2

import (
	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"

	"github.com/gofiber/fiber/v3"
)

func registerHydraOAuth2Routes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	authenticatedUser := func(handler fiber.Handler) (any, []any) {
		routeHandler, routeRest := userCoreModule.RouteHandlerParts(userCoreModule.RequireAuthenticatedUser(apiHelper), handler)
		return routeHandler, routeRest
	}

	apiHelper.Router.Get("/api/oauth2/authorize", handleHydraAuthorizeRedirect())
	apiHelper.Router.Post("/api/oauth2/token", handleHydraPublicProxy("/oauth2/token"))
	apiHelper.Router.Post("/api/oauth2/revoke", handleHydraPublicProxy("/oauth2/revoke"))

	apiHelper.Router.Get("/api/oauth2/login", handleHydraGetLoginRequest())
	loginAcceptHandler, loginAcceptRest := authenticatedUser(handleHydraAcceptLogin())
	apiHelper.Router.Post("/api/oauth2/login/accept", loginAcceptHandler, loginAcceptRest...)
	loginRejectHandler, loginRejectRest := authenticatedUser(handleHydraRejectLogin())
	apiHelper.Router.Post("/api/oauth2/login/reject", loginRejectHandler, loginRejectRest...)

	consentHandler, consentRest := authenticatedUser(handleHydraGetConsentRequest())
	apiHelper.Router.Get("/api/oauth2/consent", consentHandler, consentRest...)
	consentAcceptHandler, consentAcceptRest := authenticatedUser(handleHydraAcceptConsent(apiHelper))
	apiHelper.Router.Post("/api/oauth2/consent/accept", consentAcceptHandler, consentAcceptRest...)
	consentRejectHandler, consentRejectRest := authenticatedUser(handleHydraRejectConsent())
	apiHelper.Router.Post("/api/oauth2/consent/reject", consentRejectHandler, consentRejectRest...)

	// Legacy frontend compatibility.
	legacyConsentHandler, legacyConsentRest := authenticatedUser(handleHydraLegacyConsentDecision(apiHelper))
	apiHelper.Router.Post("/api/oauth2/authorize/consent", legacyConsentHandler, legacyConsentRest...)
}

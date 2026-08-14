package oauth2

import (
	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/oauth2"

	"github.com/gofiber/fiber/v3"
)

func registerHydraOAuth2Routes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig) {
	authenticatedUser := func(handler fiber.Handler) (any, []any) {
		routeHandler, routeRest := userCoreModule.RouteHandlerParts(userCoreModule.RequireAuthenticatedUser(apiHelper), handler)
		return routeHandler, routeRest
	}

	apiHelper.Router.Get("/api/oauth2/authorize", handleHydraAuthorizeRedirect(hydraConfig))
	apiHelper.Router.Post("/api/oauth2/token", handleHydraPublicProxy(hydraConfig, "/oauth2/token"))
	apiHelper.Router.Post("/api/oauth2/revoke", handleHydraPublicProxy(hydraConfig, "/oauth2/revoke"))

	apiHelper.Router.Get("/api/oauth2/login", handleHydraGetLoginRequest(hydraConfig))
	loginAcceptHandler, loginAcceptRest := authenticatedUser(handleHydraAcceptLogin(hydraConfig))
	apiHelper.Router.Post("/api/oauth2/login/accept", loginAcceptHandler, loginAcceptRest...)
	loginRejectHandler, loginRejectRest := authenticatedUser(handleHydraRejectLogin(hydraConfig))
	apiHelper.Router.Post("/api/oauth2/login/reject", loginRejectHandler, loginRejectRest...)

	consentHandler, consentRest := authenticatedUser(handleHydraGetConsentRequest(hydraConfig))
	apiHelper.Router.Get("/api/oauth2/consent", consentHandler, consentRest...)
	consentAcceptHandler, consentAcceptRest := authenticatedUser(handleHydraAcceptConsent(apiHelper, hydraConfig))
	apiHelper.Router.Post("/api/oauth2/consent/accept", consentAcceptHandler, consentAcceptRest...)
	consentRejectHandler, consentRejectRest := authenticatedUser(handleHydraRejectConsent(hydraConfig))
	apiHelper.Router.Post("/api/oauth2/consent/reject", consentRejectHandler, consentRejectRest...)

	// Legacy frontend compatibility.
	legacyConsentHandler, legacyConsentRest := authenticatedUser(handleHydraLegacyConsentDecision(apiHelper, hydraConfig))
	apiHelper.Router.Post("/api/oauth2/authorize/consent", legacyConsentHandler, legacyConsentRest...)
}

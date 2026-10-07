package oauth2

import (
	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"

	"github.com/gofiber/fiber/v3"
)

func registerHydraOAuth2Routes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig, deviceFlow DeviceFlowConfig) {
	authenticatedUser := func(handler fiber.Handler) (any, []any) {
		routeHandler, routeRest := userCoreModule.RouteHandlerParts(userCoreModule.RequireAuthenticatedUser(apiHelper), handler)
		return routeHandler, routeRest
	}

	apiHelper.Router.Get("/api/oauth2/authorize", handleHydraAuthorizeRedirect(hydraConfig))
	// Device routes are registered unconditionally; the handlers apply the
	// startup and runtime switches.
	deviceFlowStore := newDeviceFlowStore(apiHelper.DBManager)
	apiHelper.Router.Post("/api/oauth2/device/auth", handleHydraDeviceAuthorization(hydraConfig, deviceFlow, deviceFlowStore))
	apiHelper.Router.Post("/api/oauth2/token", handleHydraTokenEndpoint(apiHelper, hydraConfig, deviceFlow, deviceFlowStore))
	apiHelper.Router.Post("/api/oauth2/revoke", handleHydraPublicProxy(hydraConfig, "/oauth2/revoke"))
	// Browser device decisions: signed-in users only, behind the same session
	// guard as consent; the handlers never answer 401 themselves.
	deviceBrowser := newDeviceBrowserHandlers(apiHelper, hydraConfig, deviceFlow, deviceFlowStore)
	deviceLookupHandler, deviceLookupRest := authenticatedUser(deviceBrowser.handleDeviceLookup)
	apiHelper.Router.Post("/api/oauth2/device/lookup", deviceLookupHandler, deviceLookupRest...)
	deviceApproveHandler, deviceApproveRest := authenticatedUser(deviceBrowser.handleDeviceApprove)
	apiHelper.Router.Post("/api/oauth2/device/approve", deviceApproveHandler, deviceApproveRest...)
	deviceDenyHandler, deviceDenyRest := authenticatedUser(deviceBrowser.handleDeviceDeny)
	apiHelper.Router.Post("/api/oauth2/device/deny", deviceDenyHandler, deviceDenyRest...)

	apiHelper.Router.Get("/api/oauth2/login", handleHydraGetLoginRequest(hydraConfig))
	loginAcceptHandler, loginAcceptRest := authenticatedUser(handleHydraAcceptLogin(hydraConfig))
	apiHelper.Router.Post("/api/oauth2/login/accept", loginAcceptHandler, loginAcceptRest...)
	loginRejectHandler, loginRejectRest := authenticatedUser(handleHydraRejectLogin(hydraConfig))
	apiHelper.Router.Post("/api/oauth2/login/reject", loginRejectHandler, loginRejectRest...)

	// Anonymous, unlike consent: see the comment on handleHydraGetLogoutRequest.
	// A user arriving here is on their way out and may no longer have a session.
	apiHelper.Router.Get("/api/oauth2/logout", handleHydraGetLogoutRequest(hydraConfig))
	apiHelper.Router.Post("/api/oauth2/logout/accept", handleHydraAcceptLogout(hydraConfig))
	apiHelper.Router.Post("/api/oauth2/logout/reject", handleHydraRejectLogout(hydraConfig))

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

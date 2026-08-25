package oauth2

import (
	"net/http"
	"net/url"
	"strings"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/oauth2"

	"github.com/gofiber/fiber/v3"
)

// RP-Initiated Logout orchestration, mirroring the login and consent handlers.
//
// These three endpoints are ANONYMOUS on purpose, unlike consent. A logout
// challenge is reached when the user is on their way out: the Kratos session may
// already be gone, and requiring one would make logout fail exactly when it is
// most needed. The challenge itself is the credential — Hydra minted it, it is
// single-use, and it names the session being ended.
func handleHydraGetLogoutRequest(hydraConfig *harukiOAuth2.HydraConfig) fiber.Handler {
	return func(c fiber.Ctx) error {
		challenge := strings.TrimSpace(c.Query("logout_challenge"))
		if challenge == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "logout_challenge is required")
		}
		resp, err := getHydraLogoutRequest(c.Context(), hydraConfig, challenge)
		if err != nil {
			return respondHydraError(c, err, "failed to query logout request")
		}
		return harukiAPIHelper.SuccessResponse(c, "ok", resp)
	}
}

func handleHydraAcceptLogout(hydraConfig *harukiOAuth2.HydraConfig) fiber.Handler {
	return func(c fiber.Ctx) error {
		var payload hydraLogoutPayload
		if err := bindBodyIfPresent(c, &payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		payload.LogoutChallenge = normalizeChallenge(payload.LogoutChallenge, c.Query("logout_challenge"))
		if payload.LogoutChallenge == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "logoutChallenge is required")
		}

		// Accept takes no body; Hydra already knows the subject and the session
		// from the challenge.
		redirect, err := sendHydraAdminJSON(c.Context(), hydraConfig, http.MethodPut,
			"/admin/oauth2/auth/requests/logout/accept",
			url.Values{"logout_challenge": {payload.LogoutChallenge}}, nil)
		if err != nil {
			return respondHydraError(c, err, "failed to accept logout request")
		}
		return harukiAPIHelper.SuccessResponse(c, "logout accepted", redirect)
	}
}

func handleHydraRejectLogout(hydraConfig *harukiOAuth2.HydraConfig) fiber.Handler {
	return func(c fiber.Ctx) error {
		var payload hydraLogoutPayload
		if err := bindBodyIfPresent(c, &payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		payload.LogoutChallenge = normalizeChallenge(payload.LogoutChallenge, c.Query("logout_challenge"))
		if payload.LogoutChallenge == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "logoutChallenge is required")
		}

		// Reject answers 204 with an empty body — there is nowhere to send the
		// browser, because the user chose to stay signed in. Decoding it as a
		// redirect response the way accept does would fail on the empty body, so
		// this path deliberately does not use sendHydraAdminJSON.
		if _, err := sendHydraAdminRequest(c.Context(), hydraConfig, http.MethodPut,
			"/admin/oauth2/auth/requests/logout/reject",
			url.Values{"logout_challenge": {payload.LogoutChallenge}}, nil); err != nil {
			return respondHydraError(c, err, "failed to reject logout request")
		}
		return harukiAPIHelper.SuccessResponse[string](c, "logout rejected", nil)
	}
}

package oauth2

import (
	"net/http"
	"net/url"
	"strings"

	harukiAPIHelper "haruki-suite/utils/api"

	"github.com/gofiber/fiber/v3"
)

func handleHydraGetLoginRequest() fiber.Handler {
	return func(c fiber.Ctx) error {
		challenge := strings.TrimSpace(c.Query("login_challenge"))
		if challenge == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "login_challenge is required")
		}
		resp, err := getHydraLoginRequest(c.Context(), challenge)
		if err != nil {
			return respondHydraError(c, err, "failed to query login request")
		}
		return harukiAPIHelper.SuccessResponse(c, "ok", resp)
	}
}

func handleHydraAcceptLogin() fiber.Handler {
	return func(c fiber.Ctx) error {
		hydraSubject, err := CurrentHydraSubject(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}

		var payload hydraLoginAcceptPayload
		if err := bindBodyIfPresent(c, &payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		payload.LoginChallenge = normalizeChallenge(payload.LoginChallenge, c.Query("login_challenge"))
		if payload.LoginChallenge == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "loginChallenge is required")
		}
		if payload.RememberFor < 0 {
			payload.RememberFor = 0
		}

		requestBody := map[string]any{
			"subject":      hydraSubject,
			"remember":     payload.Remember,
			"remember_for": payload.RememberFor,
		}
		if payload.ACR != "" {
			requestBody["acr"] = payload.ACR
		}

		redirect, err := sendHydraAdminJSON(c.Context(), http.MethodPut, "/admin/oauth2/auth/requests/login/accept", url.Values{"login_challenge": {payload.LoginChallenge}}, requestBody)
		if err != nil {
			return respondHydraError(c, err, "failed to accept login request")
		}
		return harukiAPIHelper.SuccessResponse(c, "login accepted", redirect)
	}
}

func handleHydraRejectLogin() fiber.Handler {
	return func(c fiber.Ctx) error {
		var payload hydraLoginRejectPayload
		if err := bindBodyIfPresent(c, &payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		payload.LoginChallenge = normalizeChallenge(payload.LoginChallenge, c.Query("login_challenge"))
		if payload.LoginChallenge == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "loginChallenge is required")
		}
		if payload.Error == "" {
			payload.Error = "access_denied"
		}
		if payload.ErrorDescription == "" {
			payload.ErrorDescription = "user denied the login request"
		}
		if payload.StatusCode <= 0 {
			payload.StatusCode = fiber.StatusForbidden
		}

		redirect, err := sendHydraAdminJSON(c.Context(), http.MethodPut, "/admin/oauth2/auth/requests/login/reject", url.Values{"login_challenge": {payload.LoginChallenge}}, map[string]any{
			"error":             payload.Error,
			"error_description": payload.ErrorDescription,
			"status_code":       payload.StatusCode,
		})
		if err != nil {
			return respondHydraError(c, err, "failed to reject login request")
		}
		return harukiAPIHelper.SuccessResponse(c, "login rejected", redirect)
	}
}

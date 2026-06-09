package oauth2

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiAPIHelper "haruki-suite/utils/api"
	userSchema "haruki-suite/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v3"
)

func handleHydraGetConsentRequest() fiber.Handler {
	return func(c fiber.Ctx) error {
		challenge := strings.TrimSpace(c.Query("consent_challenge"))
		if challenge == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "consent_challenge is required")
		}
		resp, err := getHydraConsentRequest(c.Context(), challenge)
		if err != nil {
			return respondHydraError(c, err, "failed to query consent request")
		}
		if err := ensureHydraConsentSubjectMatchesCurrentUser(c, resp); err != nil {
			return respondHydraError(c, err, "failed to validate consent request subject")
		}
		return harukiAPIHelper.SuccessResponse(c, "ok", resp)
	}
}

func handleHydraAcceptConsent(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		hydraSubject, err := CurrentHydraSubject(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}

		var payload hydraConsentAcceptPayload
		if err := bindBodyIfPresent(c, &payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		payload.ConsentChallenge = normalizeChallenge(payload.ConsentChallenge, c.Query("consent_challenge"))
		if payload.ConsentChallenge == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "consentChallenge is required")
		}

		redirect, err := acceptHydraConsent(c.Context(), apiHelper, userID, hydraSubject, payload.ConsentChallenge, payload.GrantScope, payload.GrantAccessTokenAudience, payload.Remember, payload.RememberFor)
		if err != nil {
			return respondHydraError(c, err, "failed to accept consent request")
		}
		return harukiAPIHelper.SuccessResponse(c, "consent accepted", redirect)
	}
}

func handleHydraRejectConsent() fiber.Handler {
	return func(c fiber.Ctx) error {
		var payload hydraConsentRejectPayload
		if err := bindBodyIfPresent(c, &payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		payload.ConsentChallenge = normalizeChallenge(payload.ConsentChallenge, c.Query("consent_challenge"))
		if payload.ConsentChallenge == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "consentChallenge is required")
		}
		consentReq, err := getHydraConsentRequest(c.Context(), payload.ConsentChallenge)
		if err != nil {
			return respondHydraError(c, err, "failed to query consent request")
		}
		if err := ensureHydraConsentSubjectMatchesCurrentUser(c, consentReq); err != nil {
			return respondHydraError(c, err, "failed to validate consent request subject")
		}
		if payload.Error == "" {
			payload.Error = "access_denied"
		}
		if payload.ErrorDescription == "" {
			payload.ErrorDescription = "user denied the consent request"
		}
		if payload.StatusCode <= 0 {
			payload.StatusCode = fiber.StatusForbidden
		}

		redirect, err := sendHydraAdminJSON(c.Context(), http.MethodPut, "/admin/oauth2/auth/requests/consent/reject", url.Values{"consent_challenge": {payload.ConsentChallenge}}, map[string]any{
			"error":             payload.Error,
			"error_description": payload.ErrorDescription,
			"status_code":       payload.StatusCode,
		})
		if err != nil {
			return respondHydraError(c, err, "failed to reject consent request")
		}
		return harukiAPIHelper.SuccessResponse(c, "consent rejected", redirect)
	}
}

func handleHydraLegacyConsentDecision(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		hydraSubject, err := CurrentHydraSubject(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}

		var payload hydraLegacyConsentPayload
		if err := bindBodyIfPresent(c, &payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		payload.ConsentChallenge = normalizeChallenge(payload.ConsentChallenge, c.Query("consent_challenge"))
		if payload.ConsentChallenge == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "consentChallenge is required when oauth2 is backed by hydra")
		}

		if !payload.Approved {
			consentReq, err := getHydraConsentRequest(c.Context(), payload.ConsentChallenge)
			if err != nil {
				return respondHydraError(c, err, "failed to query consent request")
			}
			if err := ensureHydraConsentSubjectMatchesCurrentUser(c, consentReq); err != nil {
				return respondHydraError(c, err, "failed to validate consent request subject")
			}
			rejectResp, rejectErr := sendHydraAdminJSON(c.Context(), http.MethodPut, "/admin/oauth2/auth/requests/consent/reject", url.Values{"consent_challenge": {payload.ConsentChallenge}}, map[string]any{
				"error":             "access_denied",
				"error_description": "user denied the consent request",
				"status_code":       fiber.StatusForbidden,
			})
			if rejectErr != nil {
				return respondHydraError(c, rejectErr, "failed to reject consent request")
			}
			return harukiAPIHelper.SuccessResponse(c, "consent rejected", rejectResp)
		}

		grantScope := payload.GrantScope
		if len(grantScope) == 0 && strings.TrimSpace(payload.Scope) != "" {
			grantScope = strings.Fields(payload.Scope)
		}

		redirect, acceptErr := acceptHydraConsent(c.Context(), apiHelper, userID, hydraSubject, payload.ConsentChallenge, grantScope, payload.GrantAccessTokenAudience, payload.Remember, payload.RememberFor)
		if acceptErr != nil {
			return respondHydraError(c, acceptErr, "failed to accept consent request")
		}
		return harukiAPIHelper.SuccessResponse(c, "consent accepted", redirect)
	}
}

func acceptHydraConsent(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID string, hydraSubject string, consentChallenge string, requestedGrantScope []string, requestedAudience []string, remember bool, rememberFor int64) (*hydraRedirectResponse, error) {
	consentReq, err := getHydraConsentRequest(ctx, consentChallenge)
	if err != nil {
		return nil, err
	}
	if subject := strings.TrimSpace(consentReq.Subject); subject != "" && subject != strings.TrimSpace(hydraSubject) && subject != strings.TrimSpace(userID) {
		return nil, fiber.NewError(fiber.StatusForbidden, "consent request subject does not match current user")
	}

	grantScope, err := normalizeGrantedValues(consentReq.RequestedScope, requestedGrantScope)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid grantScope")
	}
	audience, err := normalizeGrantedValues(consentReq.RequestedAccessTokenAudience, requestedAudience)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid grantAccessTokenAudience")
	}

	dbUser, err := apiHelper.DBManager.DB.User.Query().Where(userSchema.IDEQ(userID)).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query user: %w", err)
	}

	if rememberFor < 0 {
		rememberFor = 0
	}

	idToken := map[string]any{
		"uid":  dbUser.ID,
		"name": dbUser.Name,
	}
	if dbUser.Email != "" {
		idToken["email"] = dbUser.Email
	}

	return sendHydraAdminJSON(ctx, http.MethodPut, "/admin/oauth2/auth/requests/consent/accept", url.Values{"consent_challenge": {consentChallenge}}, map[string]any{
		"grant_scope":                 grantScope,
		"grant_access_token_audience": audience,
		"remember":                    remember,
		"remember_for":                rememberFor,
		"session": map[string]any{
			"access_token": map[string]any{"uid": dbUser.ID},
			"id_token":     idToken,
		},
	})
}

func normalizeGrantedValues(allowed []string, requested []string) ([]string, error) {
	if len(requested) == 0 {
		return slices.Clone(allowed), nil
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, value := range allowed {
		allowedSet[value] = struct{}{}
	}
	values := make([]string, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))
	for _, value := range requested {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := allowedSet[value]; !ok {
			return nil, fmt.Errorf("requested value %q is not allowed by hydra challenge", value)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	if len(values) == 0 && len(allowed) > 0 {
		return nil, fmt.Errorf("at least one grant value is required")
	}
	return values, nil
}

func ensureHydraConsentSubjectMatchesCurrentUser(c fiber.Ctx, consentReq *hydraConsentRequestResponse) error {
	if consentReq == nil {
		return fiber.NewError(fiber.StatusBadGateway, "invalid consent request")
	}
	return CurrentHydraSubjectMatches(c, consentReq.Subject)
}

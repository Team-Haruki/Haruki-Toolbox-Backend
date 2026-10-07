package oauth2

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
)

func handleHydraGetConsentRequest(hydraConfig *harukiOAuth2.HydraConfig) fiber.Handler {
	return func(c fiber.Ctx) error {
		challenge := strings.TrimSpace(c.Query("consent_challenge"))
		if challenge == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "consent_challenge is required")
		}
		resp, err := getHydraConsentRequest(c.Context(), hydraConfig, challenge)
		if err != nil {
			return respondHydraError(c, err, "failed to query consent request")
		}
		if err := ensureHydraConsentSubjectMatchesCurrentUser(c, resp); err != nil {
			return respondHydraError(c, err, "failed to validate consent request subject")
		}
		if err := ensureHydraConsentNotDeviceFlow(resp); err != nil {
			return respondHydraError(c, err, "failed to query consent request")
		}
		return harukiAPIHelper.Responses.SuccessResponse(c, "ok", resp)
	}
}

func handleHydraAcceptConsent(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig) fiber.Handler {
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

		redirect, err := acceptHydraConsent(c.Context(), apiHelper, hydraConfig, userID, hydraSubject, userCoreModule.IsCurrentUserEmailVerified(c), payload.ConsentChallenge, payload.GrantScope, payload.GrantAccessTokenAudience, payload.Remember, payload.RememberFor)
		if err != nil {
			return respondHydraError(c, err, "failed to accept consent request")
		}
		return harukiAPIHelper.Responses.SuccessResponse(c, "consent accepted", redirect)
	}
}

func handleHydraRejectConsent(hydraConfig *harukiOAuth2.HydraConfig) fiber.Handler {
	return func(c fiber.Ctx) error {
		var payload hydraConsentRejectPayload
		if err := bindBodyIfPresent(c, &payload); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}
		payload.ConsentChallenge = normalizeChallenge(payload.ConsentChallenge, c.Query("consent_challenge"))
		if payload.ConsentChallenge == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "consentChallenge is required")
		}
		consentReq, err := getHydraConsentRequest(c.Context(), hydraConfig, payload.ConsentChallenge)
		if err != nil {
			return respondHydraError(c, err, "failed to query consent request")
		}
		if err := ensureHydraConsentSubjectMatchesCurrentUser(c, consentReq); err != nil {
			return respondHydraError(c, err, "failed to validate consent request subject")
		}
		if err := ensureHydraConsentNotDeviceFlow(consentReq); err != nil {
			return respondHydraError(c, err, "failed to query consent request")
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

		redirect, err := sendHydraAdminJSON(c.Context(), hydraConfig, http.MethodPut, "/admin/oauth2/auth/requests/consent/reject", url.Values{"consent_challenge": {payload.ConsentChallenge}}, map[string]any{
			"error":             payload.Error,
			"error_description": payload.ErrorDescription,
			"status_code":       payload.StatusCode,
		})
		if err != nil {
			return respondHydraError(c, err, "failed to reject consent request")
		}
		return harukiAPIHelper.Responses.SuccessResponse(c, "consent rejected", redirect)
	}
}

func handleHydraLegacyConsentDecision(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig) fiber.Handler {
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
			consentReq, err := getHydraConsentRequest(c.Context(), hydraConfig, payload.ConsentChallenge)
			if err != nil {
				return respondHydraError(c, err, "failed to query consent request")
			}
			if err := ensureHydraConsentSubjectMatchesCurrentUser(c, consentReq); err != nil {
				return respondHydraError(c, err, "failed to validate consent request subject")
			}
			if err := ensureHydraConsentNotDeviceFlow(consentReq); err != nil {
				return respondHydraError(c, err, "failed to query consent request")
			}
			rejectResp, rejectErr := sendHydraAdminJSON(c.Context(), hydraConfig, http.MethodPut, "/admin/oauth2/auth/requests/consent/reject", url.Values{"consent_challenge": {payload.ConsentChallenge}}, map[string]any{
				"error":             "access_denied",
				"error_description": "user denied the consent request",
				"status_code":       fiber.StatusForbidden,
			})
			if rejectErr != nil {
				return respondHydraError(c, rejectErr, "failed to reject consent request")
			}
			return harukiAPIHelper.Responses.SuccessResponse(c, "consent rejected", rejectResp)
		}

		grantScope := payload.GrantScope
		if len(grantScope) == 0 && strings.TrimSpace(payload.Scope) != "" {
			grantScope = strings.Fields(payload.Scope)
		}

		redirect, acceptErr := acceptHydraConsent(c.Context(), apiHelper, hydraConfig, userID, hydraSubject, userCoreModule.IsCurrentUserEmailVerified(c), payload.ConsentChallenge, grantScope, payload.GrantAccessTokenAudience, payload.Remember, payload.RememberFor)
		if acceptErr != nil {
			return respondHydraError(c, acceptErr, "failed to accept consent request")
		}
		return harukiAPIHelper.Responses.SuccessResponse(c, "consent accepted", redirect)
	}
}

func acceptHydraConsent(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig, userID string, hydraSubject string, emailVerified bool, consentChallenge string, requestedGrantScope []string, requestedAudience []string, remember bool, rememberFor int64) (*hydraRedirectResponse, error) {
	consentReq, err := getHydraConsentRequest(ctx, hydraConfig, consentChallenge)
	if err != nil {
		return nil, err
	}
	// The subject check comes first: a user who does not own the request gets
	// the subject mismatch and learns neither the flow type nor the client's
	// state.
	if subject := strings.TrimSpace(consentReq.Subject); subject != "" && subject != strings.TrimSpace(hydraSubject) && subject != strings.TrimSpace(userID) {
		return nil, fiber.NewError(fiber.StatusForbidden, "consent request subject does not match current user")
	}
	if err := ensureHydraConsentNotDeviceFlow(consentReq); err != nil {
		return nil, err
	}
	if err := ensureHydraConsentClientActive(ctx, hydraConfig, consentReq.Client.ClientID); err != nil {
		return nil, err
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

	idToken := buildHydraOIDCIDTokenClaims(dbUser.ID, dbUser.Name, dbUser.Email, emailVerified, grantScope)

	return sendHydraAdminJSON(ctx, hydraConfig, http.MethodPut, "/admin/oauth2/auth/requests/consent/accept", url.Values{"consent_challenge": {consentChallenge}}, map[string]any{
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

// errHydraConsentClientDisabled refuses consent for a disabled or deleted
// client (updatedData.code "client_disabled").
var errHydraConsentClientDisabled = &oauth2CodedError{
	Status:  fiber.StatusForbidden,
	Code:    "client_disabled",
	Message: "oauth2 client is disabled",
}

// ensureHydraConsentClientActive refuses consent for a client an admin has
// disabled. Hydra ignores metadata.haruki.active, so without this check a
// disabled client still completes the authorization-code flow and receives
// fresh tokens. A deleted client reads as disabled, so the response does not
// tell the two apart, and a failed lookup refuses rather than lets it through,
// with the bearer middleware's 503 wording.
func ensureHydraConsentClientActive(ctx context.Context, hydraConfig *harukiOAuth2.HydraConfig, clientID string) error {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return errHydraConsentClientDisabled
	}
	active, err := checkHydraOAuth2ClientActive(hydraConfig)(ctx, clientID)
	if err != nil {
		harukiLogger.Errorf("OAuth2 consent client active check failed: client=%s err=%v", clientID, err)
		return fiber.NewError(fiber.StatusServiceUnavailable, "oauth2 client validation unavailable")
	}
	if !active {
		return errHydraConsentClientDisabled
	}
	return nil
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

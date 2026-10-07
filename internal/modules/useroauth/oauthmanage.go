package useroauth

import (
	"strings"

	oauth2Module "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/oauth2"
	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
)

type oauthAuthorizationResponse struct {
	ConsentRequestID string   `json:"consentRequestId,omitempty"`
	ClientID         string   `json:"clientId"`
	ClientName       string   `json:"clientName"`
	ClientType       string   `json:"clientType"`
	Scopes           []string `json:"scopes"`
	CreatedAt        string   `json:"createdAt"`
	// FlowType is "device" or "browser"; DeviceLabel is "" for browser
	// authorizations.
	FlowType    string `json:"flowType"`
	DeviceLabel string `json:"deviceLabel"`
}

const (
	oauthAuditActionRevokeConsent = "user.oauth.authorization.revoke_consent"

	// updatedData.code of the per-device revocation (ory-suite-usage §10.5.6).
	oauthCodeAuthorizationNotFound = "authorization_not_found"
	oauthCodeRevokeFailed          = "revoke_failed"
)

// oauthErrorCodeData is updatedData of a per-device revocation error.
type oauthErrorCodeData struct {
	Code string `json:"code"`
}

type oauthRevokeConsentResponse struct {
	Revoked bool `json:"revoked"`
}

func handleListOAuthAuthorizations(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		_, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		hydraSubjects, err := oauth2Module.CurrentHydraSubjects(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		sessions, err := oauth2Module.ListHydraConsentSessionsForSubjects(ctx, hydraConfig, hydraSubjects)
		if err != nil {
			harukiLogger.Errorf("Failed to query hydra oauth consent sessions: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to query authorizations")
		}
		resp := make([]oauthAuthorizationResponse, 0, len(sessions))
		for _, session := range sessions {
			createdAt := ""
			if session.HandledAt != nil {
				createdAt = session.HandledAt.UTC().Format("2006-01-02T15:04:05Z")
			}
			resp = append(resp, oauthAuthorizationResponse{
				ConsentRequestID: session.ConsentRequestID,
				ClientID:         session.ConsentRequest.Client.ClientID,
				ClientName:       session.ConsentRequest.Client.ClientName,
				ClientType:       oauth2Module.HydraClientTypeFromAuthMethod(session.ConsentRequest.Client.TokenEndpointAuthMethod),
				Scopes:           append([]string{}, session.GrantScope...),
				CreatedAt:        createdAt,
				FlowType:         session.FlowType(),
				DeviceLabel:      session.DeviceLabel(),
			})
		}
		return harukiAPIHelper.Responses.SuccessResponse(c, "ok", &resp)
	}
}

func handleRevokeOAuthAuthorization(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		hydraSubjects, err := oauth2Module.CurrentHydraSubjects(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		clientID := c.Params("client_id")
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.oauth.authorization.revoke", result, userID, map[string]any{"reason": reason, "clientID": clientID})
		}()
		if strings.TrimSpace(clientID) != "" {
			exists, err := oauth2Module.HydraConsentSessionExistsForSubjects(ctx, hydraConfig, hydraSubjects, clientID)
			if err != nil {
				harukiLogger.Errorf("Failed to query hydra oauth consent sessions before revoke: %v", err)
				reason = "query_client_failed"
				return harukiAPIHelper.ErrorInternal(c, "failed to query client")
			}
			if !exists {
				reason = "client_not_found"
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
		}
		if _, _, err := oauth2Module.RevokeHydraConsentSessionsForSubjects(ctx, hydraConfig, clientID, hydraSubjects); err != nil {
			harukiLogger.Errorf("Failed to revoke hydra oauth consent sessions: %v", err)
			reason = "revoke_authorization_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to revoke authorization")
		}
		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.Responses.SuccessResponse[string](c, "authorization revoked", nil)
	}
}

func respondOAuthRevokeConsentError(c fiber.Ctx, status int, code, message string) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	return harukiAPIHelper.Responses.UpdatedDataResponse(c, status, message, &oauthErrorCodeData{Code: code})
}

// handleRevokeOAuthAuthorizationConsent revokes one authorization (one consent
// session, e.g. one device) of the signed-in user. Hydra answers 204 for any
// consent request ID, its own or not, so the ownership check here is the only
// guard: the user's own consent sessions are listed and the pair
// (consent_request_id, client_id) must match one of them, otherwise the answer
// is 404 without any Hydra write. The revocation then names only the matched
// session's consent request ID, which also revokes the tokens issued under it.
// Like the device browser endpoints it never answers 401 itself and never
// relays Hydra's text.
func handleRevokeOAuthAuthorizationConsent(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		consentRequestID := strings.TrimSpace(c.Params("consent_request_id"))
		userID, _ := userCoreModule.CurrentUserID(c)
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, oauthAuditActionRevokeConsent, result, userID, map[string]any{"reason": reason, "clientID": clientID, "consentRequestId": consentRequestID})
		}()
		notFound := func(why string) error {
			reason = why
			return respondOAuthRevokeConsentError(c, fiber.StatusNotFound, oauthCodeAuthorizationNotFound, "authorization not found")
		}
		if clientID == "" || consentRequestID == "" {
			return notFound("authorization_not_found")
		}
		// The group guard established the session; no subject means nothing
		// the caller could own.
		hydraSubjects, err := oauth2Module.CurrentHydraSubjects(c)
		if err != nil {
			return notFound("missing_subject")
		}
		sessions, err := oauth2Module.ListHydraConsentSessionsForSubjects(c.Context(), hydraConfig, hydraSubjects)
		if err != nil {
			harukiLogger.Errorf("Failed to query hydra oauth consent sessions before revoking one: %v", err)
			reason = "query_authorizations_failed"
			return respondOAuthRevokeConsentError(c, fiber.StatusBadGateway, oauthCodeRevokeFailed, "failed to revoke authorization")
		}
		owned := ""
		for _, session := range sessions {
			if strings.TrimSpace(session.ConsentRequestID) == consentRequestID && strings.TrimSpace(session.ConsentRequest.Client.ClientID) == clientID {
				owned = strings.TrimSpace(session.ConsentRequestID)
				break
			}
		}
		if owned == "" {
			return notFound("authorization_not_found")
		}
		if err := oauth2Module.RevokeHydraConsentSessionByID(c.Context(), hydraConfig, owned); err != nil {
			harukiLogger.Errorf("Failed to revoke hydra oauth consent session: client=%s err=%v", clientID, err)
			reason = "revoke_authorization_failed"
			return respondOAuthRevokeConsentError(c, fiber.StatusBadGateway, oauthCodeRevokeFailed, "failed to revoke authorization")
		}
		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		c.Set(fiber.HeaderCacheControl, "no-store")
		return harukiAPIHelper.Responses.SuccessResponse(c, "authorization revoked", &oauthRevokeConsentResponse{Revoked: true})
	}
}

func RegisterUserOAuthAuthorizationRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydraConfig *harukiOAuth2.HydraConfig) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id/oauth2/authorizations", userCoreModule.RouteHandlers(userCoreModule.RequireAuthenticatedSelf(apiHelper, "toolbox_user_id"))...)
	r.Get("/", handleListOAuthAuthorizations(apiHelper, hydraConfig))
	r.Delete("/:client_id", handleRevokeOAuthAuthorization(apiHelper, hydraConfig))
	r.Delete("/:client_id/consents/:consent_request_id", handleRevokeOAuthAuthorizationConsent(apiHelper, hydraConfig))
}

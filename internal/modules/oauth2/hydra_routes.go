package oauth2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiAPIHelper "haruki-suite/utils/api"
	userSchema "haruki-suite/utils/database/postgresql/user"
	harukiLogger "haruki-suite/utils/logger"
	harukiOAuth2 "haruki-suite/utils/oauth2"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v3"
)

type hydraOAuthClientDetails struct {
	ClientID   string   `json:"client_id"`
	ClientName string   `json:"client_name"`
	GrantTypes []string `json:"grant_types"`
	Scope      string   `json:"scope"`
}

type hydraLoginRequestResponse struct {
	Challenge                    string                  `json:"challenge"`
	Skip                         bool                    `json:"skip"`
	Subject                      string                  `json:"subject"`
	RequestURL                   string                  `json:"request_url"`
	RequestedScope               []string                `json:"requested_scope"`
	RequestedAccessTokenAudience []string                `json:"requested_access_token_audience"`
	Client                       hydraOAuthClientDetails `json:"client"`
}

type hydraConsentRequestResponse struct {
	Challenge                    string                  `json:"challenge"`
	Skip                         bool                    `json:"skip"`
	Subject                      string                  `json:"subject"`
	RequestURL                   string                  `json:"request_url"`
	RequestedScope               []string                `json:"requested_scope"`
	RequestedAccessTokenAudience []string                `json:"requested_access_token_audience"`
	Client                       hydraOAuthClientDetails `json:"client"`
}

type hydraRedirectResponse struct {
	RedirectTo string `json:"redirect_to"`
}

type hydraLoginAcceptPayload struct {
	LoginChallenge string `json:"loginChallenge"`
	Remember       bool   `json:"remember"`
	RememberFor    int64  `json:"rememberFor"`
	ACR            string `json:"acr"`
}

type hydraLoginRejectPayload struct {
	LoginChallenge   string `json:"loginChallenge"`
	Error            string `json:"error"`
	ErrorDescription string `json:"errorDescription"`
	StatusCode       int    `json:"statusCode"`
}

type hydraConsentAcceptPayload struct {
	ConsentChallenge         string   `json:"consentChallenge"`
	GrantScope               []string `json:"grantScope"`
	GrantAccessTokenAudience []string `json:"grantAccessTokenAudience"`
	Remember                 bool     `json:"remember"`
	RememberFor              int64    `json:"rememberFor"`
}

type hydraConsentRejectPayload struct {
	ConsentChallenge string `json:"consentChallenge"`
	Error            string `json:"error"`
	ErrorDescription string `json:"errorDescription"`
	StatusCode       int    `json:"statusCode"`
}

type hydraLegacyConsentPayload struct {
	ConsentChallenge         string   `json:"consentChallenge"`
	Approved                 bool     `json:"approved"`
	Scope                    string   `json:"scope"`
	GrantScope               []string `json:"grantScope"`
	GrantAccessTokenAudience []string `json:"grantAccessTokenAudience"`
	Remember                 bool     `json:"remember"`
	RememberFor              int64    `json:"rememberFor"`
}

type hydraErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	Message          string `json:"message"`
}

type hydraRequestError struct {
	Status  int
	Message string
}

var (
	hydraHTTPClientMu      sync.RWMutex
	hydraSharedHTTPClient  *http.Client
	hydraSharedTimeoutNano int64
)

func (e *hydraRequestError) Error() string {
	return fmt.Sprintf("hydra request failed with status %d: %s", e.Status, e.Message)
}

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

func handleHydraAuthorizeRedirect() fiber.Handler {
	return func(c fiber.Ctx) error {
		targetURL, err := harukiOAuth2.HydraBrowserEndpoint("/oauth2/auth")
		if err != nil {
			harukiLogger.Errorf("Hydra authorize endpoint is not configured: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "oauth2 provider is not configured")
		}
		rawQuery := string(c.Request().URI().QueryString())
		if rawQuery != "" {
			targetURL += "?" + rawQuery
		}
		return c.Redirect().To(targetURL)
	}
}

func handleHydraPublicProxy(endpointPath string) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetURL, err := harukiOAuth2.HydraPublicEndpoint(endpointPath)
		if err != nil {
			harukiLogger.Errorf("Hydra public endpoint is not configured: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "oauth2 provider is not configured")
		}

		rawQuery := string(c.Request().URI().QueryString())
		if rawQuery != "" {
			targetURL += "?" + rawQuery
		}

		req, err := http.NewRequestWithContext(c.Context(), c.Method(), targetURL, bytes.NewReader(c.Body()))
		if err != nil {
			harukiLogger.Errorf("Failed to create Hydra proxy request: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to build oauth2 provider request")
		}
		copyRequestHeaderIfPresent(c, req, "Authorization")
		copyRequestHeaderIfPresent(c, req, "Content-Type")
		copyRequestHeaderIfPresent(c, req, "Accept")

		resp, err := hydraHTTPClient().Do(req)
		if err != nil {
			harukiLogger.Errorf("Hydra proxy request failed: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "oauth2 provider unavailable")
		}
		defer func(body io.ReadCloser) {
			_ = body.Close()
		}(resp.Body)

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			harukiLogger.Errorf("Failed to read Hydra proxy response: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to read oauth2 provider response")
		}

		copyHydraResponseHeaders(c, resp.Header)
		c.Status(resp.StatusCode)
		if len(respBody) == 0 {
			return nil
		}
		return c.Send(respBody)
	}
}

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

func normalizeChallenge(bodyChallenge string, queryChallenge string) string {
	if strings.TrimSpace(bodyChallenge) != "" {
		return strings.TrimSpace(bodyChallenge)
	}
	return strings.TrimSpace(queryChallenge)
}

func ensureHydraConsentSubjectMatchesCurrentUser(c fiber.Ctx, consentReq *hydraConsentRequestResponse) error {
	if consentReq == nil {
		return fiber.NewError(fiber.StatusBadGateway, "invalid consent request")
	}
	return CurrentHydraSubjectMatches(c, consentReq.Subject)
}

func bindBodyIfPresent(c fiber.Ctx, payload any) error {
	if len(bytes.TrimSpace(c.Body())) == 0 {
		return nil
	}
	return c.Bind().Body(payload)
}

func getHydraLoginRequest(ctx context.Context, challenge string) (*hydraLoginRequestResponse, error) {
	response, err := sendHydraAdminRequest(ctx, http.MethodGet, "/admin/oauth2/auth/requests/login", url.Values{"login_challenge": {challenge}}, nil)
	if err != nil {
		return nil, err
	}
	var parsed hydraLoginRequestResponse
	if err := sonic.Unmarshal(response, &parsed); err != nil {
		return nil, fmt.Errorf("failed to decode hydra login request: %w", err)
	}
	return &parsed, nil
}

func getHydraConsentRequest(ctx context.Context, challenge string) (*hydraConsentRequestResponse, error) {
	response, err := sendHydraAdminRequest(ctx, http.MethodGet, "/admin/oauth2/auth/requests/consent", url.Values{"consent_challenge": {challenge}}, nil)
	if err != nil {
		return nil, err
	}
	var parsed hydraConsentRequestResponse
	if err := sonic.Unmarshal(response, &parsed); err != nil {
		return nil, fmt.Errorf("failed to decode hydra consent request: %w", err)
	}
	return &parsed, nil
}

func sendHydraAdminJSON(ctx context.Context, method string, endpointPath string, query url.Values, payload map[string]any) (*hydraRedirectResponse, error) {
	response, err := sendHydraAdminRequest(ctx, method, endpointPath, query, payload)
	if err != nil {
		return nil, err
	}
	var parsed hydraRedirectResponse
	if err := sonic.Unmarshal(response, &parsed); err != nil {
		return nil, fmt.Errorf("failed to decode hydra redirect response: %w", err)
	}
	return &parsed, nil
}

func sendHydraAdminRequest(ctx context.Context, method string, endpointPath string, query url.Values, payload map[string]any) ([]byte, error) {
	targetURL, err := harukiOAuth2.HydraAdminEndpoint(endpointPath)
	if err != nil {
		return nil, err
	}
	if encoded := query.Encode(); encoded != "" {
		targetURL += "?" + encoded
	}

	var requestBody []byte
	if payload != nil {
		requestBody, err = sonic.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to encode hydra request body: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, targetURL, bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create hydra request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if clientID, clientSecret := harukiOAuth2.HydraClientCredentials(); clientID != "" {
		req.SetBasicAuth(clientID, clientSecret)
	}

	resp, err := hydraHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call hydra: %w", err)
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read hydra response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := http.StatusText(resp.StatusCode)
		var hydraErr hydraErrorResponse
		if err := sonic.Unmarshal(body, &hydraErr); err == nil {
			for _, candidate := range []string{hydraErr.ErrorDescription, hydraErr.Message, hydraErr.Error} {
				if strings.TrimSpace(candidate) != "" {
					message = candidate
					break
				}
			}
		}
		return nil, &hydraRequestError{Status: resp.StatusCode, Message: message}
	}

	return body, nil
}

func respondHydraError(c fiber.Ctx, err error, fallback string) error {
	var reqErr *hydraRequestError
	if errors.As(err, &reqErr) {
		status := reqErr.Status
		if status < 400 || status >= 600 {
			status = fiber.StatusBadGateway
		}
		message := strings.TrimSpace(reqErr.Message)
		if message == "" {
			message = fallback
		}
		return harukiAPIHelper.UpdatedDataResponse[string](c, status, message, nil)
	}

	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
	}

	harukiLogger.Errorf("Hydra request failed: %v", err)
	return harukiAPIHelper.ErrorInternal(c, fallback)
}

func hydraHTTPClient() *http.Client {
	timeout := harukiOAuth2.HydraRequestTimeout()
	timeoutNano := timeout.Nanoseconds()

	hydraHTTPClientMu.Lock()
	defer hydraHTTPClientMu.Unlock()

	if hydraSharedHTTPClient != nil && hydraSharedTimeoutNano == timeoutNano {
		return hydraSharedHTTPClient
	}

	client := &http.Client{Timeout: timeout}
	hydraSharedHTTPClient = client
	hydraSharedTimeoutNano = timeoutNano
	return hydraSharedHTTPClient
}

func copyHydraResponseHeaders(c fiber.Ctx, header http.Header) {
	for _, name := range []string{"Content-Type", "Cache-Control", "Pragma", "WWW-Authenticate", "Location"} {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			c.Set(name, value)
		}
	}
}

func copyRequestHeaderIfPresent(c fiber.Ctx, req *http.Request, name string) {
	if value := strings.TrimSpace(c.Get(name)); value != "" {
		req.Header.Set(name, value)
	}
}

package oauth2

import (
	"fmt"
	"haruki-suite/config"
	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/oauthauthorization"
	"haruki-suite/utils/database/postgresql/oauthclient"
	"haruki-suite/utils/database/postgresql/user"
	harukiRedis "haruki-suite/utils/database/redis"
	harukiLogger "haruki-suite/utils/logger"
	harukiOAuth2 "haruki-suite/utils/oauth2"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

func handleAuthorize(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		clientID := c.Query("client_id")
		redirectURI := c.Query("redirect_uri")
		responseType := c.Query("response_type")
		scope := c.Query("scope")
		state := c.Query("state")
		codeChallenge := c.Query("code_challenge")
		codeChallengeMethod := c.Query("code_challenge_method")

		if clientID == "" || redirectURI == "" {
			return oauthError(c, fiber.StatusBadRequest, "invalid_request", "client_id and redirect_uri are required")
		}

		client, err := apiHelper.DBManager.DB.OAuthClient.Query().
			Where(oauthclient.ClientIDEQ(clientID), oauthclient.ActiveEQ(true)).
			Only(ctx)
		if err != nil {
			return oauthError(c, fiber.StatusUnauthorized, "invalid_client", "client not found or inactive")
		}

		if !slices.Contains(client.RedirectUris, redirectURI) {
			return oauthError(c, fiber.StatusBadRequest, "invalid_request", "redirect_uri not registered for this client")
		}
		if responseType != oauthResponseTypeCode {
			return errorRedirect(c, redirectURI, state, "unsupported_response_type", "only 'code' is supported")
		}
		if scope == "" {
			return errorRedirect(c, redirectURI, state, "invalid_scope", "scope is required")
		}

		if client.ClientType == oauthClientTypePublic && codeChallenge == "" {
			return errorRedirect(c, redirectURI, state, "invalid_request", "code_challenge is required for public clients (PKCE)")
		}
		if codeChallenge != "" && codeChallengeMethod != oauthPKCEChallengeMethodS256 {
			return errorRedirect(c, redirectURI, state, "invalid_request", "code_challenge_method must be S256")
		}

		requestedScopes := parseScopeList(scope)
		validatedScopes, ok := harukiOAuth2.ValidateScopes(requestedScopes, client.Scopes)
		if !ok {
			return errorRedirect(c, redirectURI, state, "invalid_scope", "one or more requested scopes are invalid")
		}

		frontendURL := config.Cfg.UserSystem.FrontendURL
		consentURL := fmt.Sprintf("%s/oauth2/consent?client_id=%s&client_name=%s&scope=%s&redirect_uri=%s&state=%s",
			strings.TrimRight(frontendURL, "/"),
			url.QueryEscape(clientID),
			url.QueryEscape(client.Name),
			url.QueryEscape(strings.Join(validatedScopes, " ")),
			url.QueryEscape(redirectURI),
			url.QueryEscape(state),
		)
		if codeChallenge != "" {
			consentURL += "&code_challenge=" + url.QueryEscape(codeChallenge)
			consentURL += "&code_challenge_method=" + oauthPKCEChallengeMethodS256
		}
		return c.Redirect().To(consentURL)
	}
}

// handleConsent processes the user's consent decision.
// POST /api/oauth2/authorize/consent
// Returns JSON { "redirect_url": "..." } for the frontend to handle the redirect.
func handleConsent(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}

		var req struct {
			ClientID            string `json:"client_id"`
			RedirectURI         string `json:"redirect_uri"`
			Scope               string `json:"scope"`
			State               string `json:"state"`
			Approved            bool   `json:"approved"`
			CodeChallenge       string `json:"code_challenge"`
			CodeChallengeMethod string `json:"code_challenge_method"`
		}
		if err := c.Bind().Body(&req); err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request body")
		}

		req.ClientID = strings.TrimSpace(req.ClientID)
		req.RedirectURI = strings.TrimSpace(req.RedirectURI)
		req.Scope = strings.TrimSpace(req.Scope)
		req.CodeChallenge = strings.TrimSpace(req.CodeChallenge)
		req.CodeChallengeMethod = strings.TrimSpace(req.CodeChallengeMethod)

		if req.ClientID == "" || req.RedirectURI == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "client_id and redirect_uri are required")
		}

		client, err := apiHelper.DBManager.DB.OAuthClient.Query().
			Where(oauthclient.ClientIDEQ(req.ClientID), oauthclient.ActiveEQ(true)).
			Only(ctx)
		if err != nil {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid client")
		}

		if !slices.Contains(client.RedirectUris, req.RedirectURI) {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid redirect_uri")
		}

		if !req.Approved {
			u, err := buildConsentDeniedRedirectURL(req.RedirectURI, req.State, client.RedirectUris)
			if err != nil {
				return harukiAPIHelper.ErrorBadRequest(c, "invalid redirect_uri")
			}
			return c.JSON(fiber.Map{"redirect_url": u})
		}

		if req.Scope == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "scope is required")
		}
		if client.ClientType == oauthClientTypePublic && req.CodeChallenge == "" {
			return harukiAPIHelper.ErrorBadRequest(c, "code_challenge is required for public clients")
		}
		if req.CodeChallenge != "" && req.CodeChallengeMethod != oauthPKCEChallengeMethodS256 {
			return harukiAPIHelper.ErrorBadRequest(c, "code_challenge_method must be S256")
		}

		requestedScopes := parseScopeList(req.Scope)
		validatedScopes, ok := harukiOAuth2.ValidateScopes(requestedScopes, client.Scopes)
		if !ok {
			return harukiAPIHelper.ErrorBadRequest(c, "invalid scope")
		}

		existing, err := apiHelper.DBManager.DB.OAuthAuthorization.Query().
			Where(
				oauthauthorization.HasUserWith(user.IDEQ(userID)),
				oauthauthorization.HasClientWith(oauthclient.IDEQ(client.ID)),
			).
			Only(ctx)
		createOnMissing, lookupErr := shouldCreateAuthorizationOnLookupErr(err)
		if lookupErr != nil {
			harukiLogger.Errorf("Failed to query oauth authorization: %v", lookupErr)
			return harukiAPIHelper.ErrorInternal(c, "failed to query authorization")
		}
		if createOnMissing {
			_, err = apiHelper.DBManager.DB.OAuthAuthorization.Create().
				SetUserID(userID).
				SetClientID(client.ID).
				SetScopes(validatedScopes).
				SetRevoked(false).
				Save(ctx)
			if err != nil {
				harukiLogger.Errorf("Failed to create oauth authorization: %v", err)
				return harukiAPIHelper.ErrorInternal(c, "failed to save authorization")
			}
		} else {
			_, err = existing.Update().
				SetScopes(validatedScopes).
				SetRevoked(false).
				Save(ctx)
			if err != nil {
				harukiLogger.Errorf("Failed to update oauth authorization: %v", err)
				return harukiAPIHelper.ErrorInternal(c, "failed to update authorization")
			}
		}

		code, err := harukiOAuth2.GenerateAuthorizationCode()
		if err != nil {
			harukiLogger.Errorf("Failed to generate auth code: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to generate authorization code")
		}

		ttl := config.Cfg.OAuth2.AuthCodeTTL
		if ttl <= 0 {
			ttl = 300
		}

		codeData := AuthCodeData{
			ClientID:            req.ClientID,
			UserID:              userID,
			RedirectURI:         req.RedirectURI,
			Scopes:              validatedScopes,
			CodeChallenge:       req.CodeChallenge,
			CodeChallengeMethod: req.CodeChallengeMethod,
		}
		codeKey := harukiRedis.BuildOAuth2AuthCodeKey(code)
		if err := apiHelper.DBManager.Redis.SetCache(ctx, codeKey, codeData, time.Duration(ttl)*time.Second); err != nil {
			harukiLogger.Errorf("Failed to store auth code in Redis: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to store authorization code")
		}

		redirectURL := buildRedirectURL(req.RedirectURI, req.State, map[string]string{
			"code": code,
		})
		return c.JSON(fiber.Map{"redirect_url": redirectURL})
	}
}

func shouldCreateAuthorizationOnLookupErr(err error) (bool, error) {
	if err == nil {
		return false, nil
	}
	if postgresql.IsNotFound(err) {
		return true, nil
	}
	return false, err
}

func buildConsentDeniedRedirectURL(redirectURI, state string, registeredRedirectURIs []string) (string, error) {
	if !slices.Contains(registeredRedirectURIs, redirectURI) {
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid redirect_uri")
	}
	return buildRedirectURL(redirectURI, state, map[string]string{
		"error":             "access_denied",
		"error_description": "user denied the request",
	}), nil
}

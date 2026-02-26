package oauth2

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"haruki-suite/config"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/oauthauthorization"
	"haruki-suite/utils/database/postgresql/oauthclient"
	"haruki-suite/utils/database/postgresql/oauthtoken"
	"haruki-suite/utils/database/postgresql/user"
	harukiLogger "haruki-suite/utils/logger"
	harukiOAuth2 "haruki-suite/utils/oauth2"
	"net/url"
	"slices"
	"strings"
	"time"

	harukiRedis "haruki-suite/utils/database/redis"

	"github.com/gofiber/fiber/v3"
	"golang.org/x/crypto/bcrypt"
)

type AuthCodeData struct {
	ClientID            string   `json:"client_id"`
	UserID              string   `json:"user_id"`
	RedirectURI         string   `json:"redirect_uri"`
	Scopes              []string `json:"scopes"`
	CodeChallenge       string   `json:"code_challenge,omitempty"`
	CodeChallengeMethod string   `json:"code_challenge_method,omitempty"`
}

func handleAuthorize(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		responseType := c.Query("response_type")
		clientID := c.Query("client_id")
		redirectURI := c.Query("redirect_uri")
		scope := c.Query("scope")
		state := c.Query("state")
		codeChallenge := c.Query("code_challenge")
		codeChallengeMethod := c.Query("code_challenge_method")

		if responseType != "code" {
			return errorRedirectOrJSON(c, redirectURI, state, "unsupported_response_type", "only 'code' is supported")
		}
		if clientID == "" || redirectURI == "" || scope == "" {
			return errorRedirectOrJSON(c, redirectURI, state, "invalid_request", "client_id, redirect_uri, and scope are required")
		}

		client, err := apiHelper.DBManager.DB.OAuthClient.Query().
			Where(oauthclient.ClientIDEQ(clientID), oauthclient.ActiveEQ(true)).
			Only(ctx)
		if err != nil {
			return errorRedirectOrJSON(c, redirectURI, state, "invalid_client", "client not found or inactive")
		}

		if !slices.Contains(client.RedirectUris, redirectURI) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error":             "invalid_request",
				"error_description": "redirect_uri not registered for this client",
			})
		}

		if client.ClientType == "public" && codeChallenge == "" {
			return errorRedirect(c, redirectURI, state, "invalid_request", "code_challenge is required for public clients (PKCE)")
		}
		if codeChallenge != "" && codeChallengeMethod != "S256" {
			return errorRedirect(c, redirectURI, state, "invalid_request", "code_challenge_method must be S256")
		}

		requestedScopes := strings.Split(scope, " ")
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
			consentURL += "&code_challenge_method=S256"
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
		userID := c.Locals("userID").(string)

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

		if !req.Approved {
			u := buildRedirectURL(req.RedirectURI, req.State, map[string]string{
				"error":             "access_denied",
				"error_description": "user denied the request",
			})
			return c.JSON(fiber.Map{"redirect_url": u})
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

		requestedScopes := strings.Split(req.Scope, " ")
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
		if err != nil {

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

// handleToken exchanges an authorization code or refresh token for an access token.
// POST /api/oauth2/token
// Body (form or JSON): grant_type, code, redirect_uri, client_id, client_secret, code_verifier, refresh_token
func handleToken(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		grantType := formOrJSON(c, "grant_type")
		clientID := formOrJSON(c, "client_id")
		clientSecret := formOrJSON(c, "client_secret")

		if clientID == "" {
			return oauthError(c, fiber.StatusBadRequest, "invalid_client", "client_id is required")
		}

		client, err := apiHelper.DBManager.DB.OAuthClient.Query().
			Where(oauthclient.ClientIDEQ(clientID), oauthclient.ActiveEQ(true)).
			Only(ctx)
		if err != nil {
			return oauthError(c, fiber.StatusUnauthorized, "invalid_client", "client not found")
		}

		if client.ClientType == "confidential" {
			if clientSecret == "" {
				return oauthError(c, fiber.StatusBadRequest, "invalid_client", "client_secret is required for confidential clients")
			}
			if err := bcrypt.CompareHashAndPassword([]byte(client.ClientSecret), []byte(clientSecret)); err != nil {
				return oauthError(c, fiber.StatusUnauthorized, "invalid_client", "invalid client credentials")
			}
		}

		switch grantType {
		case "authorization_code":
			return handleAuthCodeExchange(c, apiHelper, client)
		case "refresh_token":
			return handleRefreshTokenExchange(c, apiHelper, client)
		default:
			return oauthError(c, fiber.StatusBadRequest, "unsupported_grant_type", "only authorization_code and refresh_token are supported")
		}
	}
}

func handleAuthCodeExchange(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, client *postgresql.OAuthClient) error {
	ctx := c.Context()
	code := formOrJSON(c, "code")
	redirectURI := formOrJSON(c, "redirect_uri")
	codeVerifier := formOrJSON(c, "code_verifier")

	if code == "" || redirectURI == "" {
		return oauthError(c, fiber.StatusBadRequest, "invalid_request", "code and redirect_uri are required")
	}

	codeKey := harukiRedis.BuildOAuth2AuthCodeKey(code)
	var codeData AuthCodeData
	found, err := apiHelper.DBManager.Redis.GetCache(ctx, codeKey, &codeData)
	if err != nil || !found {
		return oauthError(c, fiber.StatusBadRequest, "invalid_grant", "authorization code not found or expired")
	}
	_ = apiHelper.DBManager.Redis.DeleteCache(ctx, codeKey)

	if codeData.ClientID != client.ClientID || codeData.RedirectURI != redirectURI {
		return oauthError(c, fiber.StatusBadRequest, "invalid_grant", "code does not match client or redirect_uri")
	}

	if codeData.CodeChallenge != "" {
		if codeVerifier == "" {
			return oauthError(c, fiber.StatusBadRequest, "invalid_request", "code_verifier is required")
		}
		if !verifyCodeChallenge(codeVerifier, codeData.CodeChallenge) {
			return oauthError(c, fiber.StatusBadRequest, "invalid_grant", "code_verifier does not match code_challenge")
		}
	} else if client.ClientType == "public" {

		return oauthError(c, fiber.StatusBadRequest, "invalid_request", "PKCE is required for public clients")
	}

	return issueTokens(c, apiHelper, client, codeData.UserID, codeData.Scopes)
}

// verifyCodeChallenge verifies PKCE S256: BASE64URL(SHA256(code_verifier)) == code_challenge
func verifyCodeChallenge(codeVerifier, codeChallenge string) bool {
	h := sha256.Sum256([]byte(codeVerifier))
	computed := base64.RawURLEncoding.EncodeToString(h[:])
	return computed == codeChallenge
}

func handleRefreshTokenExchange(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, client *postgresql.OAuthClient) error {
	ctx := c.Context()
	refreshToken := formOrJSON(c, "refresh_token")

	if refreshToken == "" {
		return oauthError(c, fiber.StatusBadRequest, "invalid_request", "refresh_token is required")
	}

	dbToken, err := apiHelper.DBManager.DB.OAuthToken.Query().
		Where(
			oauthtoken.RefreshTokenEQ(refreshToken),
			oauthtoken.RevokedEQ(false),
			oauthtoken.HasClientWith(oauthclient.ClientIDEQ(client.ClientID)),
		).
		WithUser().
		Only(ctx)
	if err != nil {
		return oauthError(c, fiber.StatusBadRequest, "invalid_grant", "refresh token not found or revoked")
	}

	_, _ = dbToken.Update().SetRevoked(true).Save(ctx)

	return issueTokens(c, apiHelper, client, dbToken.Edges.User.ID, dbToken.Scopes)
}

func issueTokens(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, client *postgresql.OAuthClient, userID string, scopes []string) error {
	ctx := c.Context()
	isConfidential := client.ClientType == "confidential"

	ttl := config.Cfg.OAuth2.AccessTokenTTL
	if ttl <= 0 {
		ttl = 3600
	}
	if isConfidential {
		ttl = 0
	}

	accessTokenStr, expiresAt, err := harukiOAuth2.GenerateAccessToken(userID, client.ClientID, scopes, ttl)
	if err != nil {
		harukiLogger.Errorf("Failed to generate access token: %v", err)
		return oauthError(c, fiber.StatusInternalServerError, "server_error", "failed to generate access token")
	}

	tokenBuilder := apiHelper.DBManager.DB.OAuthToken.Create().
		SetAccessToken(accessTokenStr).
		SetScopes(scopes).
		SetUserID(userID).
		SetClientID(client.ID)

	if expiresAt != nil {
		tokenBuilder = tokenBuilder.SetExpiresAt(*expiresAt)
	}

	var refreshTokenStr string
	refreshTokenStr, err = harukiOAuth2.GenerateRefreshToken()
	if err != nil {
		harukiLogger.Errorf("Failed to generate refresh token: %v", err)
		return oauthError(c, fiber.StatusInternalServerError, "server_error", "failed to generate refresh token")
	}
	tokenBuilder = tokenBuilder.SetRefreshToken(refreshTokenStr)

	if _, err := tokenBuilder.Save(ctx); err != nil {
		harukiLogger.Errorf("Failed to save oauth token: %v", err)
		return oauthError(c, fiber.StatusInternalServerError, "server_error", "failed to save token")
	}

	resp := fiber.Map{
		"access_token": accessTokenStr,
		"token_type":   "Bearer",
		"scope":        strings.Join(scopes, " "),
	}
	if expiresAt != nil {
		resp["expires_in"] = ttl
	}
	if refreshTokenStr != "" {
		resp["refresh_token"] = refreshTokenStr
	}

	c.Set("Cache-Control", "no-store")
	c.Set("Pragma", "no-cache")
	return c.JSON(resp)
}

// handleRevoke revokes an access token or refresh token (RFC 7009).
// POST /api/oauth2/revoke
func handleRevoke(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		token := formOrJSON(c, "token")
		clientID := formOrJSON(c, "client_id")
		clientSecret := formOrJSON(c, "client_secret")

		if token == "" || clientID == "" || clientSecret == "" {
			return oauthError(c, fiber.StatusBadRequest, "invalid_request", "token, client_id, and client_secret are required")
		}

		client, err := apiHelper.DBManager.DB.OAuthClient.Query().
			Where(oauthclient.ClientIDEQ(clientID), oauthclient.ActiveEQ(true)).
			Only(ctx)
		if err != nil {
			return oauthError(c, fiber.StatusUnauthorized, "invalid_client", "client not found")
		}
		if err := bcrypt.CompareHashAndPassword([]byte(client.ClientSecret), []byte(clientSecret)); err != nil {
			return oauthError(c, fiber.StatusUnauthorized, "invalid_client", "invalid client credentials")
		}

		_, err = apiHelper.DBManager.DB.OAuthToken.Update().
			Where(
				oauthtoken.Or(
					oauthtoken.AccessTokenEQ(token),
					oauthtoken.RefreshTokenEQ(token),
				),
				oauthtoken.HasClientWith(oauthclient.IDEQ(client.ID)),
			).
			SetRevoked(true).
			Save(ctx)
		if err != nil {
			harukiLogger.Warnf("OAuth2 revoke: token not found or already revoked: %v", err)
		}

		return c.JSON(fiber.Map{"status": "ok"})
	}
}

func formOrJSON(c fiber.Ctx, key string) string {

	val := c.FormValue(key)
	if val != "" {
		return val
	}

	return c.Query(key)
}

func oauthError(c fiber.Ctx, status int, errorCode, description string) error {
	return c.Status(status).JSON(fiber.Map{
		"error":             errorCode,
		"error_description": description,
	})
}

func errorRedirect(c fiber.Ctx, redirectURI, state, errorCode, description string) error {
	u := fmt.Sprintf("%s?error=%s&error_description=%s",
		redirectURI,
		url.QueryEscape(errorCode),
		url.QueryEscape(description),
	)
	if state != "" {
		u += "&state=" + url.QueryEscape(state)
	}
	return c.Redirect().To(u)
}

func errorRedirectOrJSON(c fiber.Ctx, redirectURI, state, errorCode, description string) error {
	if redirectURI != "" {
		return errorRedirect(c, redirectURI, state, errorCode, description)
	}
	return oauthError(c, fiber.StatusBadRequest, errorCode, description)
}

// buildRedirectURL constructs a redirect URI with the given query params and optional state.
func buildRedirectURL(baseURI, state string, params map[string]string) string {
	first := true
	u := baseURI
	for k, v := range params {
		if first {
			u += "?"
			first = false
		} else {
			u += "&"
		}
		u += url.QueryEscape(k) + "=" + url.QueryEscape(v)
	}
	if state != "" {
		if first {
			u += "?"
		} else {
			u += "&"
		}
		u += "state=" + url.QueryEscape(state)
	}
	return u
}

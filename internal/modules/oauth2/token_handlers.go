package oauth2

import (
	"crypto/sha256"
	"encoding/base64"
	"haruki-suite/config"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/oauthclient"
	"haruki-suite/utils/database/postgresql/oauthtoken"
	userSchema "haruki-suite/utils/database/postgresql/user"
	harukiRedis "haruki-suite/utils/database/redis"
	harukiLogger "haruki-suite/utils/logger"
	harukiOAuth2 "haruki-suite/utils/oauth2"
	"net/url"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v3"
	"golang.org/x/crypto/bcrypt"
)

// handleToken exchanges an authorization code or refresh token for an access token.
// POST /api/oauth2/token
// Body (application/x-www-form-urlencoded): grant_type, code, redirect_uri, client_id, client_secret, code_verifier, refresh_token
func handleToken(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		formValues, err := parseOAuthFormRequest(c)
		if err != nil {
			return err
		}

		clientAuth, errResp := extractClientAuthentication(c, formValues)
		if errResp != nil {
			return respondOAuthError(c, *errResp)
		}
		if clientAuth.ClientID == "" {
			return respondOAuthError(c, oauthErrorResponse{
				Status:               fiber.StatusUnauthorized,
				Code:                 "invalid_client",
				Description:          "client authentication is required",
				BasicChallengeNeeded: true,
			})
		}

		grantType := formValue(formValues, "grant_type")
		if grantType == "" {
			return oauthError(c, fiber.StatusBadRequest, "invalid_request", "grant_type is required")
		}

		client, err := apiHelper.DBManager.DB.OAuthClient.Query().
			Where(oauthclient.ClientIDEQ(clientAuth.ClientID), oauthclient.ActiveEQ(true)).
			Only(ctx)
		if err != nil {
			return respondOAuthError(c, oauthErrorResponse{
				Status:               fiber.StatusUnauthorized,
				Code:                 "invalid_client",
				Description:          "client not found",
				BasicChallengeNeeded: true,
			})
		}

		if client.ClientType == oauthClientTypeConfidential {
			if clientAuth.ClientSecret == "" {
				return respondOAuthError(c, oauthErrorResponse{
					Status:               fiber.StatusUnauthorized,
					Code:                 "invalid_client",
					Description:          "client_secret is required for confidential clients",
					BasicChallengeNeeded: true,
				})
			}
			if err := bcrypt.CompareHashAndPassword([]byte(client.ClientSecret), []byte(clientAuth.ClientSecret)); err != nil {
				return respondOAuthError(c, oauthErrorResponse{
					Status:               fiber.StatusUnauthorized,
					Code:                 "invalid_client",
					Description:          "invalid client credentials",
					BasicChallengeNeeded: true,
				})
			}
		}

		switch grantType {
		case oauthGrantTypeAuthorizationCode:
			return handleAuthCodeExchange(c, apiHelper, client, formValues)
		case oauthGrantTypeRefreshToken:
			return handleRefreshTokenExchange(c, apiHelper, client, formValues)
		default:
			return oauthError(c, fiber.StatusBadRequest, "unsupported_grant_type", "only authorization_code and refresh_token are supported")
		}
	}
}

func handleAuthCodeExchange(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, client *postgresql.OAuthClient, formValues url.Values) error {
	ctx := c.Context()
	code := formValue(formValues, "code")
	redirectURI := formValue(formValues, "redirect_uri")
	codeVerifier := formValue(formValues, "code_verifier")

	if code == "" || redirectURI == "" {
		return oauthError(c, fiber.StatusBadRequest, "invalid_request", "code and redirect_uri are required")
	}

	codeKey := harukiRedis.BuildOAuth2AuthCodeKey(code)
	var codeData AuthCodeData
	rawCodeData, found, err := apiHelper.DBManager.Redis.GetRawCache(ctx, codeKey)
	if err != nil || !found {
		return oauthError(c, fiber.StatusBadRequest, "invalid_grant", "authorization code not found or expired")
	}
	if err := sonic.Unmarshal([]byte(rawCodeData), &codeData); err != nil {
		harukiLogger.Errorf("Failed to decode authorization code payload: %v", err)
		return oauthError(c, fiber.StatusBadRequest, "invalid_grant", "authorization code not found or expired")
	}

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
	} else if client.ClientType == oauthClientTypePublic {
		return oauthError(c, fiber.StatusBadRequest, "invalid_request", "PKCE is required for public clients")
	}
	consumed, err := apiHelper.DBManager.Redis.DeleteCacheIfValueMatches(ctx, codeKey, rawCodeData)
	if err != nil {
		harukiLogger.Errorf("Failed to consume authorization code: %v", err)
		return oauthError(c, fiber.StatusInternalServerError, "server_error", "failed to consume authorization code")
	}
	if !consumed {
		return oauthError(c, fiber.StatusBadRequest, "invalid_grant", "authorization code already used or invalid")
	}

	return issueTokens(c, apiHelper, client, codeData.UserID, codeData.Scopes)
}

// verifyCodeChallenge verifies PKCE S256: BASE64URL(SHA256(code_verifier)) == code_challenge
func verifyCodeChallenge(codeVerifier, codeChallenge string) bool {
	h := sha256.Sum256([]byte(codeVerifier))
	computed := base64.RawURLEncoding.EncodeToString(h[:])
	return computed == codeChallenge
}

func handleRefreshTokenExchange(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, client *postgresql.OAuthClient, formValues url.Values) error {
	ctx := c.Context()
	refreshToken := formValue(formValues, "refresh_token")

	if refreshToken == "" {
		return oauthError(c, fiber.StatusBadRequest, "invalid_request", "refresh_token is required")
	}

	dbToken, err := apiHelper.DBManager.DB.OAuthToken.Query().
		Where(
			oauthtoken.RefreshTokenEQ(refreshToken),
			oauthtoken.RevokedEQ(false),
			oauthtoken.HasClientWith(oauthclient.ClientIDEQ(client.ClientID)),
			oauthtoken.HasUserWith(userSchema.BannedEQ(false)),
		).
		WithUser().
		Only(ctx)
	if err != nil {
		return oauthError(c, fiber.StatusBadRequest, "invalid_grant", "refresh token not found or revoked")
	}

	refreshTTL := config.Cfg.OAuth2.RefreshTokenTTL
	if refreshTTL <= 0 {
		refreshTTL = 30 * 24 * 3600
	}
	if dbToken.CreatedAt.Add(time.Duration(refreshTTL) * time.Second).Before(time.Now()) {
		if _, revokeErr := dbToken.Update().SetRevoked(true).Save(ctx); revokeErr != nil {
			harukiLogger.Errorf("Failed to revoke expired refresh token: %v", revokeErr)
			return oauthError(c, fiber.StatusInternalServerError, "server_error", "failed to revoke expired refresh token")
		}
		return oauthError(c, fiber.StatusBadRequest, "invalid_grant", "refresh token expired")
	}

	issuedScopes := dbToken.Scopes
	scopeParam := formValue(formValues, "scope")
	if scopeParam != "" {
		requestedScopes := parseScopeList(scopeParam)
		if len(requestedScopes) == 0 {
			return oauthError(c, fiber.StatusBadRequest, "invalid_scope", "invalid scope")
		}
		if !isScopeSubset(requestedScopes, dbToken.Scopes) {
			return oauthError(c, fiber.StatusBadRequest, "invalid_scope", "requested scope exceeds originally granted scope")
		}
		issuedScopes = requestedScopes
	}

	return rotateRefreshTokenAndIssueTokens(c, apiHelper, client, dbToken, issuedScopes)
}

func issueTokens(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, client *postgresql.OAuthClient, userID string, scopes []string) error {
	ctx := c.Context()
	if err := ensureOAuth2TokenSubjectActive(c, apiHelper, userID); err != nil {
		return err
	}

	ttl := config.Cfg.OAuth2.AccessTokenTTL
	if ttl <= 0 {
		ttl = 3600
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

	return sendTokenResponse(c, accessTokenStr, refreshTokenStr, scopes, ttl, expiresAt)
}

func rotateRefreshTokenAndIssueTokens(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, client *postgresql.OAuthClient, dbToken *postgresql.OAuthToken, scopes []string) error {
	ctx := c.Context()
	if dbToken == nil || dbToken.Edges.User == nil || dbToken.Edges.User.Banned {
		return oauthError(c, fiber.StatusBadRequest, "invalid_grant", "token subject is invalid")
	}

	ttl := config.Cfg.OAuth2.AccessTokenTTL
	if ttl <= 0 {
		ttl = 3600
	}

	accessTokenStr, expiresAt, err := harukiOAuth2.GenerateAccessToken(dbToken.Edges.User.ID, client.ClientID, scopes, ttl)
	if err != nil {
		harukiLogger.Errorf("Failed to generate access token: %v", err)
		return oauthError(c, fiber.StatusInternalServerError, "server_error", "failed to generate access token")
	}

	refreshTokenStr, err := harukiOAuth2.GenerateRefreshToken()
	if err != nil {
		harukiLogger.Errorf("Failed to generate refresh token: %v", err)
		return oauthError(c, fiber.StatusInternalServerError, "server_error", "failed to generate refresh token")
	}

	tx, err := apiHelper.DBManager.DB.Tx(ctx)
	if err != nil {
		harukiLogger.Errorf("Failed to start token rotation transaction: %v", err)
		return oauthError(c, fiber.StatusInternalServerError, "server_error", "failed to rotate refresh token")
	}

	updatedRows, err := tx.OAuthToken.Update().
		Where(
			oauthtoken.IDEQ(dbToken.ID),
			oauthtoken.RevokedEQ(false),
		).
		SetRevoked(true).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		harukiLogger.Errorf("Failed to revoke existing refresh token: %v", err)
		return oauthError(c, fiber.StatusInternalServerError, "server_error", "failed to rotate refresh token")
	}
	if updatedRows != 1 {
		_ = tx.Rollback()
		return oauthError(c, fiber.StatusBadRequest, "invalid_grant", "refresh token already used or revoked")
	}

	tokenBuilder := tx.OAuthToken.Create().
		SetAccessToken(accessTokenStr).
		SetScopes(scopes).
		SetUserID(dbToken.Edges.User.ID).
		SetClientID(client.ID).
		SetRefreshToken(refreshTokenStr)
	if expiresAt != nil {
		tokenBuilder = tokenBuilder.SetExpiresAt(*expiresAt)
	}

	if _, err := tokenBuilder.Save(ctx); err != nil {
		_ = tx.Rollback()
		harukiLogger.Errorf("Failed to save rotated oauth token: %v", err)
		return oauthError(c, fiber.StatusInternalServerError, "server_error", "failed to save token")
	}

	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
		harukiLogger.Errorf("Failed to commit token rotation transaction: %v", err)
		return oauthError(c, fiber.StatusInternalServerError, "server_error", "failed to rotate refresh token")
	}

	return sendTokenResponse(c, accessTokenStr, refreshTokenStr, scopes, ttl, expiresAt)
}

func ensureOAuth2TokenSubjectActive(c fiber.Ctx, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID string) error {
	dbUser, err := apiHelper.DBManager.DB.User.Query().
		Where(userSchema.IDEQ(userID)).
		Select(userSchema.FieldBanned).
		Only(c.Context())
	if err != nil {
		return oauthError(c, fiber.StatusBadRequest, "invalid_grant", "token subject is invalid")
	}
	if dbUser.Banned {
		return oauthError(c, fiber.StatusBadRequest, "invalid_grant", "token subject is invalid")
	}
	return nil
}

func sendTokenResponse(c fiber.Ctx, accessTokenStr, refreshTokenStr string, scopes []string, ttl int, expiresAt *time.Time) error {
	resp := fiber.Map{
		"access_token": accessTokenStr,
		"token_type":   oauthTokenTypeBearer,
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

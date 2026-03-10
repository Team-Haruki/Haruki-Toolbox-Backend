package oauth2

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql/oauthclient"
	"haruki-suite/utils/database/postgresql/oauthtoken"
	"haruki-suite/utils/database/postgresql/predicate"
	harukiLogger "haruki-suite/utils/logger"

	"github.com/gofiber/fiber/v3"
	"golang.org/x/crypto/bcrypt"
)

// handleRevoke revokes an access token or refresh token (RFC 7009).
// POST /api/oauth2/revoke
func handleRevoke(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
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

		token := formValue(formValues, "token")
		if token == "" {
			return oauthError(c, fiber.StatusBadRequest, "invalid_request", "token is required")
		}
		if clientAuth.ClientID == "" {
			return respondOAuthError(c, oauthErrorResponse{
				Status:               fiber.StatusUnauthorized,
				Code:                 "invalid_client",
				Description:          "client authentication is required",
				BasicChallengeNeeded: true,
			})
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

		tokenTypeHint := formValue(formValues, "token_type_hint")
		var predicates []predicate.OAuthToken
		switch tokenTypeHint {
		case oauthTokenTypeHintAccessToken:
			predicates = append(predicates, oauthtoken.AccessTokenEQ(token))
		case oauthTokenTypeHintRefreshToken:
			predicates = append(predicates, oauthtoken.RefreshTokenEQ(token))
		default:
			predicates = append(predicates, oauthtoken.Or(
				oauthtoken.AccessTokenEQ(token),
				oauthtoken.RefreshTokenEQ(token),
			))
		}
		predicates = append(predicates, oauthtoken.HasClientWith(oauthclient.IDEQ(client.ID)))

		_, err = apiHelper.DBManager.DB.OAuthToken.Update().Where(predicates...).
			SetRevoked(true).
			Save(ctx)
		if err != nil {
			harukiLogger.Warnf("OAuth2 revoke: token not found or already revoked: %v", err)
		}

		return c.SendStatus(fiber.StatusOK)
	}
}

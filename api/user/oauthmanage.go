package user

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql/oauthauthorization"
	"haruki-suite/utils/database/postgresql/oauthclient"
	"haruki-suite/utils/database/postgresql/oauthtoken"
	"haruki-suite/utils/database/postgresql/user"
	harukiLogger "haruki-suite/utils/logger"

	"github.com/gofiber/fiber/v3"
)

type oauthAuthorizationResponse struct {
	ClientID   string   `json:"clientId"`
	ClientName string   `json:"clientName"`
	ClientType string   `json:"clientType"`
	Scopes     []string `json:"scopes"`
	CreatedAt  string   `json:"createdAt"`
}

func handleListOAuthAuthorizations(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID := c.Locals("userID").(string)

		authorizations, err := apiHelper.DBManager.DB.OAuthAuthorization.Query().
			Where(
				oauthauthorization.HasUserWith(user.IDEQ(userID)),
				oauthauthorization.RevokedEQ(false),
			).
			WithClient().
			All(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to query oauth authorizations: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to query authorizations")
		}

		resp := make([]oauthAuthorizationResponse, 0, len(authorizations))
		for _, auth := range authorizations {
			if auth.Edges.Client == nil {
				continue
			}
			resp = append(resp, oauthAuthorizationResponse{
				ClientID:   auth.Edges.Client.ClientID,
				ClientName: auth.Edges.Client.Name,
				ClientType: auth.Edges.Client.ClientType,
				Scopes:     auth.Scopes,
				CreatedAt:  auth.CreatedAt.Format("2006-01-02T15:04:05Z"),
			})
		}

		return harukiAPIHelper.SuccessResponse(c, "ok", &resp)
	}
}

func handleRevokeOAuthAuthorization(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID := c.Locals("userID").(string)
		clientID := c.Params("client_id")

		client, err := apiHelper.DBManager.DB.OAuthClient.Query().
			Where(oauthclient.ClientIDEQ(clientID)).
			Only(ctx)
		if err != nil {
			return harukiAPIHelper.ErrorNotFound(c, "client not found")
		}

		_, err = apiHelper.DBManager.DB.OAuthAuthorization.Update().
			Where(
				oauthauthorization.HasUserWith(user.IDEQ(userID)),
				oauthauthorization.HasClientWith(oauthclient.IDEQ(client.ID)),
			).
			SetRevoked(true).
			Save(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to revoke oauth authorization: %v", err)
			return harukiAPIHelper.ErrorInternal(c, "failed to revoke authorization")
		}

		_, err = apiHelper.DBManager.DB.OAuthToken.Update().
			Where(
				oauthtoken.HasUserWith(user.IDEQ(userID)),
				oauthtoken.HasClientWith(oauthclient.IDEQ(client.ID)),
			).
			SetRevoked(true).
			Save(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to revoke oauth tokens: %v", err)
		}

		return harukiAPIHelper.SuccessResponse[string](c, "authorization revoked", nil)
	}
}

func registerOAuthAuthorizationRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id/oauth2/authorizations")

	r.Get("/", apiHelper.SessionHandler.VerifySessionToken, checkUserNotBanned(apiHelper), handleListOAuthAuthorizations(apiHelper))
	r.Delete("/:client_id", apiHelper.SessionHandler.VerifySessionToken, checkUserNotBanned(apiHelper), handleRevokeOAuthAuthorization(apiHelper))
}

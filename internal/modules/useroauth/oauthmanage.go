package useroauth

import (
	oauth2Module "haruki-suite/internal/modules/oauth2"
	userCoreModule "haruki-suite/internal/modules/usercore"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/oauthauthorization"
	"haruki-suite/utils/database/postgresql/oauthclient"
	"haruki-suite/utils/database/postgresql/oauthtoken"
	userSchema "haruki-suite/utils/database/postgresql/user"
	harukiLogger "haruki-suite/utils/logger"
	"strings"

	"github.com/gofiber/fiber/v3"
)

type oauthAuthorizationResponse struct {
	ConsentRequestID string   `json:"consentRequestId,omitempty"`
	ClientID         string   `json:"clientId"`
	ClientName       string   `json:"clientName"`
	ClientType       string   `json:"clientType"`
	Scopes           []string `json:"scopes"`
	CreatedAt        string   `json:"createdAt"`
}

func handleListOAuthAuthorizations(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx := c.Context()
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		if oauth2Module.HydraOAuthManagementEnabled() {
			sessions, err := oauth2Module.ListHydraConsentSessions(ctx, userID)
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
					Scopes:           append([]string(nil), session.GrantScope...),
					CreatedAt:        createdAt,
				})
			}
			return harukiAPIHelper.SuccessResponse(c, "ok", &resp)
		}

		authorizations, err := apiHelper.DBManager.DB.OAuthAuthorization.Query().
			Where(
				oauthauthorization.HasUserWith(userSchema.IDEQ(userID)),
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
		userID, err := userCoreModule.CurrentUserID(c)
		if err != nil {
			return harukiAPIHelper.ErrorUnauthorized(c, "user not authenticated")
		}
		clientID := c.Params("client_id")
		result := harukiAPIHelper.SystemLogResultFailure
		reason := "unknown"
		defer func() {
			userCoreModule.WriteUserAuditLog(c, apiHelper, "user.oauth.authorization.revoke", result, userID, map[string]any{
				"reason":   reason,
				"clientID": clientID,
			})
		}()
		if oauth2Module.HydraOAuthManagementEnabled() {
			if strings.TrimSpace(clientID) != "" {
				exists, err := oauth2Module.HydraConsentSessionExistsForClient(ctx, userID, clientID)
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
			if err := oauth2Module.RevokeHydraConsentSessions(ctx, userID, clientID); err != nil {
				harukiLogger.Errorf("Failed to revoke hydra oauth consent sessions: %v", err)
				reason = "revoke_authorization_failed"
				return harukiAPIHelper.ErrorInternal(c, "failed to revoke authorization")
			}
			result = harukiAPIHelper.SystemLogResultSuccess
			reason = "ok"
			return harukiAPIHelper.SuccessResponse[string](c, "authorization revoked", nil)
		}

		client, err := apiHelper.DBManager.DB.OAuthClient.Query().
			Where(oauthclient.ClientIDEQ(clientID)).
			Only(ctx)
		if err != nil {
			if !postgresql.IsNotFound(err) {
				harukiLogger.Errorf("Failed to query oauth client %s: %v", clientID, err)
				reason = "query_client_failed"
				return harukiAPIHelper.ErrorInternal(c, "failed to query client")
			}
			reason = "client_not_found"
			return harukiAPIHelper.ErrorNotFound(c, "client not found")
		}

		tx, err := apiHelper.DBManager.DB.Tx(ctx)
		if err != nil {
			harukiLogger.Errorf("Failed to start oauth revoke transaction: %v", err)
			reason = "start_transaction_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to revoke authorization")
		}
		if _, err := tx.OAuthAuthorization.Update().
			Where(
				oauthauthorization.HasUserWith(userSchema.IDEQ(userID)),
				oauthauthorization.HasClientWith(oauthclient.IDEQ(client.ID)),
			).
			SetRevoked(true).
			Save(ctx); err != nil {
			_ = tx.Rollback()
			harukiLogger.Errorf("Failed to revoke oauth authorization in transaction: %v", err)
			reason = "revoke_authorization_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to revoke authorization")
		}
		if _, err := tx.OAuthToken.Update().
			Where(
				oauthtoken.HasUserWith(userSchema.IDEQ(userID)),
				oauthtoken.HasClientWith(oauthclient.IDEQ(client.ID)),
			).
			SetRevoked(true).
			Save(ctx); err != nil {
			_ = tx.Rollback()
			harukiLogger.Errorf("Failed to revoke oauth tokens in transaction: %v", err)
			reason = "revoke_tokens_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to revoke authorization")
		}
		if err := tx.Commit(); err != nil {
			_ = tx.Rollback()
			harukiLogger.Errorf("Failed to commit oauth revoke transaction: %v", err)
			reason = "commit_failed"
			return harukiAPIHelper.ErrorInternal(c, "failed to revoke authorization")
		}

		result = harukiAPIHelper.SystemLogResultSuccess
		reason = "ok"
		return harukiAPIHelper.SuccessResponse[string](c, "authorization revoked", nil)
	}
}

func RegisterUserOAuthAuthorizationRoutes(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	r := apiHelper.Router.Group("/api/user/:toolbox_user_id/oauth2/authorizations")

	r.Get("/", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleListOAuthAuthorizations(apiHelper))
	r.Delete("/:client_id", apiHelper.SessionHandler.VerifySessionToken, userCoreModule.RequireSelfUserParam("toolbox_user_id"), userCoreModule.CheckUserNotBanned(apiHelper), handleRevokeOAuthAuthorization(apiHelper))
}

package admin

import (
	"context"
	"encoding/json"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/oauthauthorization"
	"haruki-suite/utils/database/postgresql/oauthclient"
	"haruki-suite/utils/database/postgresql/oauthtoken"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"mime"
	"net/url"
	"strings"
	"time"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

type adminOAuthTokenStats struct {
	Total           int        `json:"total"`
	Active          int        `json:"active"`
	Revoked         int        `json:"revoked"`
	LatestIssuedAt  *time.Time `json:"latestIssuedAt,omitempty"`
	LatestExpiresAt *time.Time `json:"latestExpiresAt,omitempty"`
}

type adminOAuthAuthorizationListItem struct {
	AuthorizationID int                  `json:"authorizationId"`
	ClientID        string               `json:"clientId"`
	ClientName      string               `json:"clientName"`
	ClientType      string               `json:"clientType"`
	ClientActive    bool                 `json:"clientActive"`
	Scopes          []string             `json:"scopes"`
	CreatedAt       time.Time            `json:"createdAt"`
	Revoked         bool                 `json:"revoked"`
	TokenStats      adminOAuthTokenStats `json:"tokenStats"`
}

type adminOAuthAuthorizationListResponse struct {
	GeneratedAt    time.Time                         `json:"generatedAt"`
	UserID         string                            `json:"userId"`
	IncludeRevoked bool                              `json:"includeRevoked"`
	Total          int                               `json:"total"`
	Items          []adminOAuthAuthorizationListItem `json:"items"`
}

type adminRevokeOAuthResponse struct {
	UserID                string  `json:"userId"`
	ClientID              *string `json:"clientId,omitempty"`
	RevokedAuthorizations int     `json:"revokedAuthorizations"`
	RevokedTokens         int     `json:"revokedTokens"`
}

func parseAdminOAuthIncludeRevoked(raw string) (bool, error) {
	includeRevoked, err := parseOptionalBoolField(raw, "include_revoked")
	if err != nil {
		return false, err
	}
	if includeRevoked == nil {
		return false, nil
	}
	return *includeRevoked, nil
}

func looksLikeJSONBody(body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return false
	}
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

func looksLikeFormBody(body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return false
	}
	return strings.Contains(trimmed, "=")
}

func parseRevokeOAuthClientIDFromJSON(body []byte) (string, error) {
	var payload struct {
		ClientID      string `json:"clientId"`
		ClientIDSnake string `json:"client_id"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	clientID := strings.TrimSpace(payload.ClientID)
	if clientID == "" {
		clientID = strings.TrimSpace(payload.ClientIDSnake)
	}
	return clientID, nil
}

func parseRevokeOAuthClientIDFromForm(body []byte) (string, error) {
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid form payload")
	}

	clientID := strings.TrimSpace(values.Get("client_id"))
	if clientID == "" {
		clientID = strings.TrimSpace(values.Get("clientId"))
	}
	return clientID, nil
}

func parseAdminRevokeOAuthClientID(c fiber.Ctx) (string, error) {
	body := c.Body()
	if len(body) == 0 || strings.TrimSpace(string(body)) == "" {
		return "", nil
	}

	rawContentType := strings.TrimSpace(c.Get("Content-Type"))
	if rawContentType == "" {
		if looksLikeJSONBody(body) {
			return parseRevokeOAuthClientIDFromJSON(body)
		}
		if looksLikeFormBody(body) {
			return parseRevokeOAuthClientIDFromForm(body)
		}
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	mediaType, _, err := mime.ParseMediaType(rawContentType)
	if err != nil {
		return "", fiber.NewError(fiber.StatusBadRequest, "invalid Content-Type")
	}

	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "application/json":
		return parseRevokeOAuthClientIDFromJSON(body)
	case "application/x-www-form-urlencoded":
		return parseRevokeOAuthClientIDFromForm(body)
	default:
		return "", fiber.NewError(fiber.StatusBadRequest, "unsupported Content-Type")
	}
}

func queryUserOAuthTokenStats(ctx context.Context, db *postgresql.Client, userID string, clientDBID int) (adminOAuthTokenStats, error) {
	base := db.OAuthToken.Query().Where(
		oauthtoken.HasUserWith(userSchema.IDEQ(userID)),
		oauthtoken.HasClientWith(oauthclient.IDEQ(clientDBID)),
	)

	total, err := base.Clone().Count(ctx)
	if err != nil {
		return adminOAuthTokenStats{}, err
	}
	active, err := base.Clone().Where(oauthtoken.RevokedEQ(false)).Count(ctx)
	if err != nil {
		return adminOAuthTokenStats{}, err
	}
	if active > total {
		active = total
	}

	stats := adminOAuthTokenStats{
		Total:   total,
		Active:  active,
		Revoked: total - active,
	}

	latestToken, err := base.Clone().Order(
		oauthtoken.ByCreatedAt(sql.OrderDesc()),
		oauthtoken.ByID(sql.OrderDesc()),
	).First(ctx)
	if err != nil {
		if postgresql.IsNotFound(err) {
			return stats, nil
		}
		return adminOAuthTokenStats{}, err
	}

	issuedAt := latestToken.CreatedAt.UTC()
	stats.LatestIssuedAt = &issuedAt
	if latestToken.ExpiresAt != nil {
		expiresAt := latestToken.ExpiresAt.UTC()
		stats.LatestExpiresAt = &expiresAt
	}
	return stats, nil
}

func handleListUserOAuthAuthorizations(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetUserID := strings.TrimSpace(c.Params("target_user_id"))
		if targetUserID == "" {
			writeAdminAuditLog(c, apiHelper, "admin.user.oauth.list", "user", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_target_user_id", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "target_user_id is required")
		}

		actorUserID, actorRole, err := currentAdminActor(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.oauth.list", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_user_session", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		targetUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(targetUserID)).
			Select(userSchema.FieldID, userSchema.FieldRole).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, "admin.user.oauth.list", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("target_user_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			writeAdminAuditLog(c, apiHelper, "admin.user.oauth.list", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_target_user_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}

		if err := ensureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.oauth.list", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("permission_denied", map[string]any{
				"actorRole":  actorRole,
				"targetRole": normalizeRole(string(targetUser.Role)),
			}))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorForbidden(c, "insufficient permissions")
		}

		includeRevoked, err := parseAdminOAuthIncludeRevoked(c.Query("include_revoked"))
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.oauth.list", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_include_revoked", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid include_revoked filter")
		}

		authQuery := apiHelper.DBManager.DB.OAuthAuthorization.Query().
			Where(oauthauthorization.HasUserWith(userSchema.IDEQ(targetUser.ID)))
		if !includeRevoked {
			authQuery = authQuery.Where(oauthauthorization.RevokedEQ(false))
		}

		authorizations, err := authQuery.
			WithClient().
			Order(
				oauthauthorization.ByCreatedAt(sql.OrderDesc()),
				oauthauthorization.ByID(sql.OrderDesc()),
			).
			All(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.oauth.list", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_authorizations_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth authorizations")
		}

		items := make([]adminOAuthAuthorizationListItem, 0, len(authorizations))
		for _, auth := range authorizations {
			if auth.Edges.Client == nil {
				continue
			}

			tokenStats, statsErr := queryUserOAuthTokenStats(c.Context(), apiHelper.DBManager.DB, targetUser.ID, auth.Edges.Client.ID)
			if statsErr != nil {
				writeAdminAuditLog(c, apiHelper, "admin.user.oauth.list", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_token_stats_failed", map[string]any{
					"clientId": auth.Edges.Client.ClientID,
				}))
				return harukiAPIHelper.ErrorInternal(c, "failed to query oauth token stats")
			}

			items = append(items, adminOAuthAuthorizationListItem{
				AuthorizationID: auth.ID,
				ClientID:        auth.Edges.Client.ClientID,
				ClientName:      auth.Edges.Client.Name,
				ClientType:      auth.Edges.Client.ClientType,
				ClientActive:    auth.Edges.Client.Active,
				Scopes:          append([]string(nil), auth.Scopes...),
				CreatedAt:       auth.CreatedAt.UTC(),
				Revoked:         auth.Revoked,
				TokenStats:      tokenStats,
			})
		}

		resp := adminOAuthAuthorizationListResponse{
			GeneratedAt:    time.Now().UTC(),
			UserID:         targetUser.ID,
			IncludeRevoked: includeRevoked,
			Total:          len(items),
			Items:          items,
		}

		writeAdminAuditLog(c, apiHelper, "admin.user.oauth.list", "user", targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"includeRevoked": includeRevoked,
			"itemCount":      len(items),
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleRevokeUserOAuth(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		targetUserID := strings.TrimSpace(c.Params("target_user_id"))
		if targetUserID == "" {
			writeAdminAuditLog(c, apiHelper, "admin.user.oauth.revoke", "user", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_target_user_id", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "target_user_id is required")
		}

		actorUserID, actorRole, err := currentAdminActor(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.oauth.revoke", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_user_session", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		targetUser, err := apiHelper.DBManager.DB.User.Query().
			Where(userSchema.IDEQ(targetUserID)).
			Select(userSchema.FieldID, userSchema.FieldRole).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, "admin.user.oauth.revoke", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("target_user_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "user not found")
			}
			writeAdminAuditLog(c, apiHelper, "admin.user.oauth.revoke", "user", targetUserID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_target_user_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
		}

		if err := ensureAdminCanManageTargetUser(actorUserID, actorRole, targetUser.ID, string(targetUser.Role)); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.oauth.revoke", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("permission_denied", map[string]any{
				"actorRole":  actorRole,
				"targetRole": normalizeRole(string(targetUser.Role)),
			}))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorForbidden(c, "insufficient permissions")
		}

		clientID, err := parseAdminRevokeOAuthClientID(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.oauth.revoke", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		var client *postgresql.OAuthClient
		if clientID != "" {
			client, err = apiHelper.DBManager.DB.OAuthClient.Query().
				Where(oauthclient.ClientIDEQ(clientID)).
				Only(c.Context())
			if err != nil {
				if postgresql.IsNotFound(err) {
					writeAdminAuditLog(c, apiHelper, "admin.user.oauth.revoke", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("client_not_found", map[string]any{
						"clientId": clientID,
					}))
					return harukiAPIHelper.ErrorNotFound(c, "client not found")
				}
				writeAdminAuditLog(c, apiHelper, "admin.user.oauth.revoke", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_client_failed", nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
			}
		}

		tx, err := apiHelper.DBManager.DB.Tx(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.oauth.revoke", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("start_transaction_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to start transaction")
		}

		authUpdate := tx.OAuthAuthorization.Update().
			Where(oauthauthorization.HasUserWith(userSchema.IDEQ(targetUser.ID)))
		tokenUpdate := tx.OAuthToken.Update().
			Where(oauthtoken.HasUserWith(userSchema.IDEQ(targetUser.ID)))
		if client != nil {
			authUpdate = authUpdate.Where(oauthauthorization.HasClientWith(oauthclient.IDEQ(client.ID)))
			tokenUpdate = tokenUpdate.Where(oauthtoken.HasClientWith(oauthclient.IDEQ(client.ID)))
		}

		revokedAuthorizations, err := authUpdate.SetRevoked(true).Save(c.Context())
		if err != nil {
			_ = tx.Rollback()
			writeAdminAuditLog(c, apiHelper, "admin.user.oauth.revoke", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("revoke_authorizations_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to revoke oauth authorizations")
		}

		revokedTokens, err := tokenUpdate.SetRevoked(true).Save(c.Context())
		if err != nil {
			_ = tx.Rollback()
			writeAdminAuditLog(c, apiHelper, "admin.user.oauth.revoke", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("revoke_tokens_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to revoke oauth tokens")
		}

		if err := tx.Commit(); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.user.oauth.revoke", "user", targetUser.ID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("commit_transaction_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to commit oauth revoke")
		}

		var responseClientID *string
		metadata := map[string]any{
			"revokedAuthorizations": revokedAuthorizations,
			"revokedTokens":         revokedTokens,
		}
		if clientID != "" {
			responseClientID = &clientID
			metadata["clientId"] = clientID
		}

		resp := adminRevokeOAuthResponse{
			UserID:                targetUser.ID,
			ClientID:              responseClientID,
			RevokedAuthorizations: revokedAuthorizations,
			RevokedTokens:         revokedTokens,
		}

		writeAdminAuditLog(c, apiHelper, "admin.user.oauth.revoke", "user", targetUser.ID, harukiAPIHelper.SystemLogResultSuccess, metadata)
		return harukiAPIHelper.SuccessResponse(c, "oauth authorizations revoked", &resp)
	}
}

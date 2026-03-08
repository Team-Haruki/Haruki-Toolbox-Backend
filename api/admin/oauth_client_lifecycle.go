package admin

import (
	"encoding/json"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/oauthauthorization"
	"haruki-suite/utils/database/postgresql/oauthclient"
	"haruki-suite/utils/database/postgresql/oauthtoken"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"math"
	"mime"
	"net/url"
	"strings"
	"time"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

const (
	defaultAdminOAuthClientAuthorizationPage     = 1
	defaultAdminOAuthClientAuthorizationPageSize = 50
	maxAdminOAuthClientAuthorizationPageSize     = 200
)

type adminOAuthClientAuthorizationsFilters struct {
	IncludeRevoked bool
	Page           int
	PageSize       int
}

type adminOAuthClientAuthorizationUser struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	Banned bool   `json:"banned"`
}

type adminOAuthClientAuthorizationListItem struct {
	AuthorizationID int                               `json:"authorizationId"`
	User            adminOAuthClientAuthorizationUser `json:"user"`
	Scopes          []string                          `json:"scopes"`
	CreatedAt       time.Time                         `json:"createdAt"`
	Revoked         bool                              `json:"revoked"`
	TokenStats      adminOAuthTokenStats              `json:"tokenStats"`
}

type adminOAuthClientAuthorizationsResponse struct {
	GeneratedAt    time.Time                               `json:"generatedAt"`
	ClientID       string                                  `json:"clientId"`
	ClientName     string                                  `json:"clientName"`
	IncludeRevoked bool                                    `json:"includeRevoked"`
	Page           int                                     `json:"page"`
	PageSize       int                                     `json:"pageSize"`
	Total          int                                     `json:"total"`
	TotalPages     int                                     `json:"totalPages"`
	HasMore        bool                                    `json:"hasMore"`
	Items          []adminOAuthClientAuthorizationListItem `json:"items"`
}

type adminOAuthClientRevokeOptions struct {
	TargetUserID         string `json:"targetUserId,omitempty"`
	RevokeAuthorizations bool   `json:"revokeAuthorizations"`
	RevokeTokens         bool   `json:"revokeTokens"`
}

type adminOAuthClientRevokeResponse struct {
	ClientID              string  `json:"clientId"`
	TargetUserID          *string `json:"targetUserId,omitempty"`
	RevokeAuthorizations  bool    `json:"revokeAuthorizations"`
	RevokeTokens          bool    `json:"revokeTokens"`
	RevokedAuthorizations int     `json:"revokedAuthorizations"`
	RevokedTokens         int     `json:"revokedTokens"`
}

type adminOAuthClientRestoreResponse struct {
	ClientID string `json:"clientId"`
	Active   bool   `json:"active"`
}

func parseAdminOAuthClientAuthorizationsFilters(c fiber.Ctx) (*adminOAuthClientAuthorizationsFilters, error) {
	includeRevoked, err := parseAdminOAuthIncludeRevoked(c.Query("include_revoked"))
	if err != nil {
		return nil, err
	}

	page, err := parsePositiveInt(c.Query("page"), defaultAdminOAuthClientAuthorizationPage, "page")
	if err != nil {
		return nil, err
	}
	pageSize, err := parsePositiveInt(c.Query("page_size"), defaultAdminOAuthClientAuthorizationPageSize, "page_size")
	if err != nil {
		return nil, err
	}
	if pageSize > maxAdminOAuthClientAuthorizationPageSize {
		return nil, fiber.NewError(fiber.StatusBadRequest, "page_size exceeds max allowed size")
	}

	return &adminOAuthClientAuthorizationsFilters{
		IncludeRevoked: includeRevoked,
		Page:           page,
		PageSize:       pageSize,
	}, nil
}

func parseAdminOAuthClientRevokeOptionsFromJSON(body []byte) (adminOAuthClientRevokeOptions, error) {
	options := adminOAuthClientRevokeOptions{
		RevokeAuthorizations: true,
		RevokeTokens:         true,
	}

	var payload struct {
		TargetUserID              string `json:"targetUserId"`
		TargetUserIDSnake         string `json:"target_user_id"`
		RevokeAuthorizations      *bool  `json:"revokeAuthorizations"`
		RevokeAuthorizationsSnake *bool  `json:"revoke_authorizations"`
		RevokeTokens              *bool  `json:"revokeTokens"`
		RevokeTokensSnake         *bool  `json:"revoke_tokens"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return adminOAuthClientRevokeOptions{}, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	targetUserID := strings.TrimSpace(payload.TargetUserID)
	if targetUserID == "" {
		targetUserID = strings.TrimSpace(payload.TargetUserIDSnake)
	}
	options.TargetUserID = targetUserID

	if payload.RevokeAuthorizations != nil {
		options.RevokeAuthorizations = *payload.RevokeAuthorizations
	} else if payload.RevokeAuthorizationsSnake != nil {
		options.RevokeAuthorizations = *payload.RevokeAuthorizationsSnake
	}
	if payload.RevokeTokens != nil {
		options.RevokeTokens = *payload.RevokeTokens
	} else if payload.RevokeTokensSnake != nil {
		options.RevokeTokens = *payload.RevokeTokensSnake
	}

	return options, nil
}

func parseAdminOAuthClientRevokeOptionsFromForm(body []byte) (adminOAuthClientRevokeOptions, error) {
	options := adminOAuthClientRevokeOptions{
		RevokeAuthorizations: true,
		RevokeTokens:         true,
	}

	values, err := url.ParseQuery(string(body))
	if err != nil {
		return adminOAuthClientRevokeOptions{}, fiber.NewError(fiber.StatusBadRequest, "invalid form payload")
	}

	targetUserID := strings.TrimSpace(values.Get("target_user_id"))
	if targetUserID == "" {
		targetUserID = strings.TrimSpace(values.Get("targetUserId"))
	}
	options.TargetUserID = targetUserID

	revokeAuthorizationsRaw := strings.TrimSpace(values.Get("revoke_authorizations"))
	if revokeAuthorizationsRaw == "" {
		revokeAuthorizationsRaw = strings.TrimSpace(values.Get("revokeAuthorizations"))
	}
	revokeAuthorizations, err := parseOptionalBoolField(revokeAuthorizationsRaw, "revoke_authorizations")
	if err != nil {
		return adminOAuthClientRevokeOptions{}, err
	}
	if revokeAuthorizations != nil {
		options.RevokeAuthorizations = *revokeAuthorizations
	}

	revokeTokensRaw := strings.TrimSpace(values.Get("revoke_tokens"))
	if revokeTokensRaw == "" {
		revokeTokensRaw = strings.TrimSpace(values.Get("revokeTokens"))
	}
	revokeTokens, err := parseOptionalBoolField(revokeTokensRaw, "revoke_tokens")
	if err != nil {
		return adminOAuthClientRevokeOptions{}, err
	}
	if revokeTokens != nil {
		options.RevokeTokens = *revokeTokens
	}

	return options, nil
}

func parseAdminOAuthClientRevokeOptions(c fiber.Ctx) (adminOAuthClientRevokeOptions, error) {
	body := c.Body()
	if len(body) == 0 || strings.TrimSpace(string(body)) == "" {
		return adminOAuthClientRevokeOptions{RevokeAuthorizations: true, RevokeTokens: true}, nil
	}

	rawContentType := strings.TrimSpace(c.Get("Content-Type"))
	if rawContentType == "" {
		if looksLikeJSONBody(body) {
			return parseAdminOAuthClientRevokeOptionsFromJSON(body)
		}
		if looksLikeFormBody(body) {
			return parseAdminOAuthClientRevokeOptionsFromForm(body)
		}
		return adminOAuthClientRevokeOptions{}, fiber.NewError(fiber.StatusBadRequest, "invalid request payload")
	}

	mediaType, _, err := mime.ParseMediaType(rawContentType)
	if err != nil {
		return adminOAuthClientRevokeOptions{}, fiber.NewError(fiber.StatusBadRequest, "invalid Content-Type")
	}

	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "application/json":
		return parseAdminOAuthClientRevokeOptionsFromJSON(body)
	case "application/x-www-form-urlencoded":
		return parseAdminOAuthClientRevokeOptionsFromForm(body)
	default:
		return adminOAuthClientRevokeOptions{}, fiber.NewError(fiber.StatusBadRequest, "unsupported Content-Type")
	}
}

func handleListOAuthClientAuthorizations(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		if clientID == "" {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.authorizations.list", "oauth_client", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_client_id", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id is required")
		}

		filters, err := parseAdminOAuthClientAuthorizationsFilters(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.authorizations.list", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_query_filters", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid query filters")
		}

		dbClient, err := apiHelper.DBManager.DB.OAuthClient.Query().
			Where(oauthclient.ClientIDEQ(clientID)).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, "admin.oauth_client.authorizations.list", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("client_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.authorizations.list", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_client_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}

		baseQuery := apiHelper.DBManager.DB.OAuthAuthorization.Query().
			Where(oauthauthorization.HasClientWith(oauthclient.IDEQ(dbClient.ID)))
		if !filters.IncludeRevoked {
			baseQuery = baseQuery.Where(oauthauthorization.RevokedEQ(false))
		}

		total, err := baseQuery.Clone().Count(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.authorizations.list", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("count_authorizations_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to count oauth authorizations")
		}

		offset := (filters.Page - 1) * filters.PageSize
		rows, err := baseQuery.Clone().
			WithUser().
			Order(
				oauthauthorization.ByCreatedAt(sql.OrderDesc()),
				oauthauthorization.ByID(sql.OrderDesc()),
			).
			Limit(filters.PageSize).
			Offset(offset).
			All(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.authorizations.list", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_authorizations_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth authorizations")
		}

		items := make([]adminOAuthClientAuthorizationListItem, 0, len(rows))
		for _, row := range rows {
			if row.Edges.User == nil {
				continue
			}

			tokenStats, statsErr := queryUserOAuthTokenStats(c.Context(), apiHelper.DBManager.DB, row.Edges.User.ID, dbClient.ID)
			if statsErr != nil {
				writeAdminAuditLog(c, apiHelper, "admin.oauth_client.authorizations.list", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_token_stats_failed", nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to query oauth token stats")
			}

			items = append(items, adminOAuthClientAuthorizationListItem{
				AuthorizationID: row.ID,
				User: adminOAuthClientAuthorizationUser{
					UserID: row.Edges.User.ID,
					Name:   row.Edges.User.Name,
					Email:  row.Edges.User.Email,
					Role:   normalizeRole(string(row.Edges.User.Role)),
					Banned: row.Edges.User.Banned,
				},
				Scopes:     append([]string(nil), row.Scopes...),
				CreatedAt:  row.CreatedAt.UTC(),
				Revoked:    row.Revoked,
				TokenStats: tokenStats,
			})
		}

		totalPages := 0
		if total > 0 {
			totalPages = int(math.Ceil(float64(total) / float64(filters.PageSize)))
		}

		resp := adminOAuthClientAuthorizationsResponse{
			GeneratedAt:    time.Now().UTC(),
			ClientID:       dbClient.ClientID,
			ClientName:     dbClient.Name,
			IncludeRevoked: filters.IncludeRevoked,
			Page:           filters.Page,
			PageSize:       filters.PageSize,
			Total:          total,
			TotalPages:     totalPages,
			HasMore:        filters.Page*filters.PageSize < total,
			Items:          items,
		}

		writeAdminAuditLog(c, apiHelper, "admin.oauth_client.authorizations.list", "oauth_client", clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"includeRevoked": filters.IncludeRevoked,
			"total":          total,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleRevokeOAuthClient(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		if clientID == "" {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.revoke", "oauth_client", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_client_id", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id is required")
		}

		_, _, err := currentAdminActor(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.revoke", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_user_session", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		options, err := parseAdminOAuthClientRevokeOptions(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.revoke", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		if !options.RevokeAuthorizations && !options.RevokeTokens {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.revoke", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("nothing_to_revoke", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "at least one revoke option must be true")
		}

		dbClient, err := apiHelper.DBManager.DB.OAuthClient.Query().
			Where(oauthclient.ClientIDEQ(clientID)).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, "admin.oauth_client.revoke", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("client_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.revoke", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_client_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}

		if options.TargetUserID != "" {
			if _, err := apiHelper.DBManager.DB.User.Query().Where(userSchema.IDEQ(options.TargetUserID)).Only(c.Context()); err != nil {
				if postgresql.IsNotFound(err) {
					writeAdminAuditLog(c, apiHelper, "admin.oauth_client.revoke", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("target_user_not_found", map[string]any{"targetUserID": options.TargetUserID}))
					return harukiAPIHelper.ErrorNotFound(c, "target user not found")
				}
				writeAdminAuditLog(c, apiHelper, "admin.oauth_client.revoke", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_target_user_failed", nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to query target user")
			}
		}

		tx, err := apiHelper.DBManager.DB.Tx(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.revoke", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("start_transaction_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to start transaction")
		}

		baseAuthUpdate := tx.OAuthAuthorization.Update().
			Where(oauthauthorization.HasClientWith(oauthclient.IDEQ(dbClient.ID)))
		baseTokenUpdate := tx.OAuthToken.Update().
			Where(oauthtoken.HasClientWith(oauthclient.IDEQ(dbClient.ID)))
		if options.TargetUserID != "" {
			baseAuthUpdate = baseAuthUpdate.Where(oauthauthorization.HasUserWith(userSchema.IDEQ(options.TargetUserID)))
			baseTokenUpdate = baseTokenUpdate.Where(oauthtoken.HasUserWith(userSchema.IDEQ(options.TargetUserID)))
		}

		revokedAuthorizations := 0
		if options.RevokeAuthorizations {
			revokedAuthorizations, err = baseAuthUpdate.SetRevoked(true).Save(c.Context())
			if err != nil {
				_ = tx.Rollback()
				writeAdminAuditLog(c, apiHelper, "admin.oauth_client.revoke", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("revoke_authorizations_failed", nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to revoke oauth authorizations")
			}
		}

		revokedTokens := 0
		if options.RevokeTokens {
			revokedTokens, err = baseTokenUpdate.SetRevoked(true).Save(c.Context())
			if err != nil {
				_ = tx.Rollback()
				writeAdminAuditLog(c, apiHelper, "admin.oauth_client.revoke", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("revoke_tokens_failed", nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to revoke oauth tokens")
			}
		}

		if err := tx.Commit(); err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.revoke", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("commit_transaction_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to commit oauth revoke")
		}

		var targetUserID *string
		if options.TargetUserID != "" {
			target := options.TargetUserID
			targetUserID = &target
		}

		resp := adminOAuthClientRevokeResponse{
			ClientID:              dbClient.ClientID,
			TargetUserID:          targetUserID,
			RevokeAuthorizations:  options.RevokeAuthorizations,
			RevokeTokens:          options.RevokeTokens,
			RevokedAuthorizations: revokedAuthorizations,
			RevokedTokens:         revokedTokens,
		}

		metadata := map[string]any{
			"revokeAuthorizations":  options.RevokeAuthorizations,
			"revokeTokens":          options.RevokeTokens,
			"revokedAuthorizations": revokedAuthorizations,
			"revokedTokens":         revokedTokens,
		}
		if options.TargetUserID != "" {
			metadata["targetUserID"] = options.TargetUserID
		}
		writeAdminAuditLog(c, apiHelper, "admin.oauth_client.revoke", "oauth_client", clientID, harukiAPIHelper.SystemLogResultSuccess, metadata)
		return harukiAPIHelper.SuccessResponse(c, "oauth client authorizations revoked", &resp)
	}
}

func handleRestoreOAuthClient(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		if clientID == "" {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.restore", "oauth_client", "", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_client_id", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id is required")
		}

		_, _, err := currentAdminActor(c)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.restore", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("missing_user_session", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorUnauthorized(c, "missing user session")
		}

		dbClient, err := apiHelper.DBManager.DB.OAuthClient.Query().
			Where(oauthclient.ClientIDEQ(clientID)).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				writeAdminAuditLog(c, apiHelper, "admin.oauth_client.restore", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("client_not_found", nil))
				return harukiAPIHelper.ErrorNotFound(c, "client not found")
			}
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.restore", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("query_client_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}

		updatedClient, err := apiHelper.DBManager.DB.OAuthClient.UpdateOneID(dbClient.ID).
			SetActive(true).
			Save(c.Context())
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.oauth_client.restore", "oauth_client", clientID, harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("restore_client_failed", nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to restore oauth client")
		}

		resp := adminOAuthClientRestoreResponse{ClientID: updatedClient.ClientID, Active: updatedClient.Active}
		writeAdminAuditLog(c, apiHelper, "admin.oauth_client.restore", "oauth_client", clientID, harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse(c, "oauth client restored", &resp)
	}
}

package adminwebhook

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	webhookModule "haruki-suite/internal/modules/webhook"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/webhookendpoint"
	"haruki-suite/utils/database/postgresql/webhooksubscription"
	harukiHandler "haruki-suite/utils/handler"
	"strings"
	"time"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

type adminWebhookSettingsPayload struct {
	Enabled   *bool   `json:"enabled,omitempty"`
	JWTSecret *string `json:"jwtSecret,omitempty"`
}

type adminWebhookSettingsResponse struct {
	Enabled             bool `json:"enabled"`
	JWTSecretConfigured bool `json:"jwtSecretConfigured"`
}

type adminWebhookPayload struct {
	ID          *string `json:"id,omitempty"`
	Credential  *string `json:"credential,omitempty"`
	CallbackURL *string `json:"callbackUrl,omitempty"`
	Bearer      *string `json:"bearer,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`
	ClearBearer bool    `json:"clearBearer,omitempty"`
}

type adminWebhookItem struct {
	ID                string     `json:"id"`
	Credential        string     `json:"credential"`
	CallbackURL       string     `json:"callbackUrl"`
	Bearer            *string    `json:"bearer,omitempty"`
	Enabled           bool       `json:"enabled"`
	SubscriptionCount int        `json:"subscriptionCount"`
	CreatedAt         *time.Time `json:"createdAt,omitempty"`
}

type adminWebhookListResponse struct {
	GeneratedAt time.Time          `json:"generatedAt"`
	Total       int                `json:"total"`
	Items       []adminWebhookItem `json:"items"`
}

type adminWebhookSubscriberItem struct {
	UserID    string     `json:"userId"`
	Server    string     `json:"server"`
	DataType  string     `json:"dataType"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
}

type adminWebhookMutationResponse struct {
	Webhook         adminWebhookItem `json:"webhook"`
	Token           string           `json:"token"`
	TokenHeaderName string           `json:"tokenHeaderName"`
}

type adminWebhookSubscribersResponse struct {
	GeneratedAt time.Time                    `json:"generatedAt"`
	WebhookID   string                       `json:"webhookId"`
	Total       int                          `json:"total"`
	Items       []adminWebhookSubscriberItem `json:"items"`
}

func buildAdminWebhookSettingsResponse(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) adminWebhookSettingsResponse {
	return adminWebhookSettingsResponse{
		Enabled:             apiHelper.GetWebhookEnabled(),
		JWTSecretConfigured: strings.TrimSpace(apiHelper.GetWebhookJWTSecret()) != "",
	}
}

func buildAdminWebhookItem(row *postgresql.WebhookEndpoint) adminWebhookItem {
	item := adminWebhookItem{
		ID:                row.ID,
		Credential:        row.Credential,
		CallbackURL:       row.CallbackURL,
		Enabled:           row.Enabled,
		SubscriptionCount: len(row.Edges.Subscriptions),
	}
	if row.Bearer != nil && strings.TrimSpace(*row.Bearer) != "" {
		bearer := strings.TrimSpace(*row.Bearer)
		item.Bearer = &bearer
	}
	createdAt := row.CreatedAt.UTC()
	item.CreatedAt = &createdAt
	return item
}

func buildAdminWebhookMutationResponse(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, row *postgresql.WebhookEndpoint) adminWebhookMutationResponse {
	resp := adminWebhookMutationResponse{
		Webhook:         buildAdminWebhookItem(row),
		TokenHeaderName: webhookModule.TokenHeaderName,
	}
	secret := strings.TrimSpace(apiHelper.GetWebhookJWTSecret())
	if secret == "" {
		return resp
	}
	token, err := webhookModule.SignWebhookToken(secret, row.ID, row.Credential)
	if err == nil {
		resp.Token = token
	}
	return resp
}

func handleGetAdminWebhookSettings(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		resp := buildAdminWebhookSettingsResponse(apiHelper)
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionGetSettings, adminWebhookTargetType, adminWebhookSettingsTargetID, harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleUpdateAdminWebhookSettings(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		var payload adminWebhookSettingsPayload
		if err := c.Bind().Body(&payload); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionUpdateSettings, adminWebhookTargetType, adminWebhookSettingsTargetID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonInvalidRequestPayload, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		update := harukiAPIHelper.RuntimeConfigUpdate{}
		jwtSecret, err := sanitizeWebhookJWTSecret(payload.JWTSecret)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionUpdateSettings, adminWebhookTargetType, adminWebhookSettingsTargetID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonInvalidWebhookJWTSecret, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid jwtSecret")
		}
		if jwtSecret != nil {
			update.WebhookJWTSecret = jwtSecret
		}
		if payload.Enabled != nil {
			update.WebhookEnabled = payload.Enabled
		}
		if err := apiHelper.UpdateRuntimeConfig(update); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionUpdateSettings, adminWebhookTargetType, adminWebhookSettingsTargetID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonPersistRuntimeConfigFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to persist webhook settings")
		}

		resp := buildAdminWebhookSettingsResponse(apiHelper)
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionUpdateSettings, adminWebhookTargetType, adminWebhookSettingsTargetID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"updatedEnabled":   payload.Enabled != nil,
			"updatedJWTSecret": jwtSecret != nil,
		})
		return harukiAPIHelper.SuccessResponse(c, "webhook settings updated", &resp)
	}
}

func handleListAdminWebhooks(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		rows, err := apiHelper.DBManager.DB.WebhookEndpoint.Query().
			WithSubscriptions().
			Order(webhookendpoint.ByCreatedAt(sql.OrderDesc()), webhookendpoint.ByID(sql.OrderAsc())).
			All(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionList, adminWebhookTargetType, adminWebhookTargetIDAll, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonQueryWebhooksFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query webhooks")
		}

		items := make([]adminWebhookItem, 0, len(rows))
		for _, row := range rows {
			if row == nil {
				continue
			}
			items = append(items, buildAdminWebhookItem(row))
		}

		resp := adminWebhookListResponse{
			GeneratedAt: adminWebhookNowUTC(),
			Total:       len(items),
			Items:       items,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionList, adminWebhookTargetType, adminWebhookTargetIDAll, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"total": resp.Total,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleCreateAdminWebhook(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		var payload adminWebhookPayload
		if err := c.Bind().Body(&payload); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionCreate, adminWebhookTargetType, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonInvalidRequestPayload, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		webhookID := ""
		if payload.ID != nil && strings.TrimSpace(*payload.ID) != "" {
			sanitizedID, err := sanitizeWebhookID(*payload.ID)
			if err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionCreate, adminWebhookTargetType, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonInvalidWebhookID, nil))
				return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid id")
			}
			webhookID = sanitizedID
		} else {
			nextID, err := resolveNextWebhookID(apiHelper, c)
			if err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionCreate, adminWebhookTargetType, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonResolveNextWebhookIDFailed, nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to create webhook")
			}
			webhookID = nextID
		}

		if payload.CallbackURL == nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionCreate, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonInvalidCallbackURL, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "callbackUrl is required")
		}
		callbackURL, ok := harukiHandler.ValidateWebhookCallbackURL(*payload.CallbackURL)
		if !ok {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionCreate, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonInvalidCallbackURL, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid callbackUrl")
		}

		credential := ""
		if payload.Credential != nil && strings.TrimSpace(*payload.Credential) != "" {
			sanitizedCredential, err := sanitizeWebhookCredential(*payload.Credential)
			if err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionCreate, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonInvalidCredential, nil))
				return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid credential")
			}
			credential = sanitizedCredential
		} else {
			generatedCredential, err := generateWebhookCredential()
			if err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionCreate, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonGenerateCredentialFailed, nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to create webhook")
			}
			credential = generatedCredential
		}

		builder := apiHelper.DBManager.DB.WebhookEndpoint.Create().
			SetID(webhookID).
			SetCredential(credential).
			SetCallbackURL(callbackURL)
		if payload.Enabled == nil {
			builder.SetEnabled(true)
		} else {
			builder.SetEnabled(*payload.Enabled)
		}
		if bearer := sanitizeOptionalBearer(payload.Bearer); bearer != nil {
			builder.SetBearer(*bearer)
		}

		created, err := builder.Save(c.Context())
		if err != nil {
			if postgresql.IsConstraintError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionCreate, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonWebhookConflict, nil))
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "webhook conflict", nil)
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionCreate, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonCreateWebhookFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to create webhook")
		}

		resp := buildAdminWebhookMutationResponse(apiHelper, created)
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionCreate, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"generatedID":         payload.ID == nil || strings.TrimSpace(*payload.ID) == "",
			"generatedCredential": payload.Credential == nil || strings.TrimSpace(*payload.Credential) == "",
		})
		return harukiAPIHelper.SuccessResponse(c, "webhook created", &resp)
	}
}

func handleUpdateAdminWebhook(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		webhookID, err := sanitizeWebhookID(c.Params("webhook_id"))
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionUpdate, adminWebhookTargetType, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonInvalidWebhookID, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid webhook_id")
		}

		var payload adminWebhookPayload
		if err := c.Bind().Body(&payload); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionUpdate, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonInvalidRequestPayload, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		current, err := queryWebhookEndpointOrNotFound(apiHelper, c, webhookID)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok && fiberErr.Code == fiber.StatusNotFound {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionUpdate, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonWebhookNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "webhook not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionUpdate, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonQueryWebhooksFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query webhook")
		}

		update := apiHelper.DBManager.DB.WebhookEndpoint.UpdateOneID(webhookID)
		changed := false
		if payload.CallbackURL != nil {
			callbackURL, ok := harukiHandler.ValidateWebhookCallbackURL(*payload.CallbackURL)
			if !ok {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionUpdate, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonInvalidCallbackURL, nil))
				return harukiAPIHelper.ErrorBadRequest(c, "invalid callbackUrl")
			}
			update.SetCallbackURL(callbackURL)
			changed = true
		}
		if payload.Credential != nil {
			credential, err := sanitizeWebhookCredential(*payload.Credential)
			if err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionUpdate, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonInvalidCredential, nil))
				return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid credential")
			}
			update.SetCredential(credential)
			changed = true
		}
		if payload.Enabled != nil {
			update.SetEnabled(*payload.Enabled)
			changed = true
		}
		if payload.ClearBearer {
			update.ClearBearer()
			changed = true
		} else if payload.Bearer != nil {
			if bearer := sanitizeOptionalBearer(payload.Bearer); bearer != nil {
				update.SetBearer(*bearer)
			} else {
				update.ClearBearer()
			}
			changed = true
		}
		if !changed {
			resp := buildAdminWebhookMutationResponse(apiHelper, current)
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionUpdate, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
				"noChange": true,
			})
			return harukiAPIHelper.SuccessResponse(c, "webhook updated", &resp)
		}

		updated, err := update.Save(c.Context())
		if err != nil {
			if postgresql.IsConstraintError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionUpdate, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonWebhookConflict, nil))
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "webhook conflict", nil)
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionUpdate, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonUpdateWebhookFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to update webhook")
		}

		updated.Edges.Subscriptions = current.Edges.Subscriptions
		resp := buildAdminWebhookMutationResponse(apiHelper, updated)
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionUpdate, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"updatedCredential": payload.Credential != nil,
			"updatedCallback":   payload.CallbackURL != nil,
			"updatedEnabled":    payload.Enabled != nil,
			"updatedBearer":     payload.ClearBearer || payload.Bearer != nil,
		})
		return harukiAPIHelper.SuccessResponse(c, "webhook updated", &resp)
	}
}

func handleDeleteAdminWebhook(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		webhookID, err := sanitizeWebhookID(c.Params("webhook_id"))
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionDelete, adminWebhookTargetType, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonInvalidWebhookID, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid webhook_id")
		}

		err = apiHelper.DBManager.DB.WebhookEndpoint.DeleteOneID(webhookID).Exec(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionDelete, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonWebhookNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "webhook not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionDelete, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonDeleteWebhookFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to delete webhook")
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionDelete, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultSuccess, nil)
		return harukiAPIHelper.SuccessResponse[string](c, "webhook deleted", nil)
	}
}

func handleListAdminWebhookSubscribers(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		webhookID, err := sanitizeWebhookID(c.Params("webhook_id"))
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionSubscribers, adminWebhookTargetType, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonInvalidWebhookID, nil))
			return adminCoreModule.RespondFiberOrBadRequest(c, err, "invalid webhook_id")
		}

		exists, err := apiHelper.DBManager.DB.WebhookEndpoint.Query().
			Where(webhookendpoint.IDEQ(webhookID)).
			Exist(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionSubscribers, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonQueryWebhooksFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query webhook")
		}
		if !exists {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionSubscribers, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonWebhookNotFound, nil))
			return harukiAPIHelper.ErrorNotFound(c, "webhook not found")
		}

		rows, err := apiHelper.DBManager.DB.WebhookSubscription.Query().
			Where(webhooksubscription.WebhookIDEQ(webhookID)).
			Order(webhooksubscription.ByCreatedAt(sql.OrderDesc()), webhooksubscription.ByID(sql.OrderAsc())).
			All(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionSubscribers, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminWebhookFailureReasonQuerySubscribersFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query webhook subscribers")
		}

		items := make([]adminWebhookSubscriberItem, 0, len(rows))
		for _, row := range rows {
			if row == nil {
				continue
			}
			item := adminWebhookSubscriberItem{
				UserID:   row.UserID,
				Server:   row.Server,
				DataType: row.DataType,
			}
			createdAt := row.CreatedAt.UTC()
			item.CreatedAt = &createdAt
			items = append(items, item)
		}

		resp := adminWebhookSubscribersResponse{
			GeneratedAt: adminWebhookNowUTC(),
			WebhookID:   webhookID,
			Total:       len(items),
			Items:       items,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminWebhookActionSubscribers, adminWebhookTargetType, webhookID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"total": resp.Total,
		})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

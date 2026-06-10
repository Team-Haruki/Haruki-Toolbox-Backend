package adminoauth

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	oauth2Module "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/oauth2"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/oauth2clientwebhookendpoint"
	"strings"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

func handleListHydraOAuthClientWebhooks(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		if clientID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookList, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingClientID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id is required")
		}

		if _, err := oauth2Module.GetHydraOAuthClient(c.Context(), clientID); err != nil {
			if oauth2Module.IsHydraNotFoundError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookList, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorNotFound(c, "oauth client not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookList, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}

		rows, err := apiHelper.DBManager.DB.OAuth2ClientWebhookEndpoint.Query().
			Where(oauth2clientwebhookendpoint.ClientIDEQ(clientID)).
			Order(oauth2clientwebhookendpoint.ByCreatedAt(sql.OrderDesc()), oauth2clientwebhookendpoint.ByID(sql.OrderAsc())).
			All(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookList, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryWebhooksFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client webhooks")
		}

		items := make([]adminOAuthClientWebhookItem, 0, len(rows))
		for _, row := range rows {
			items = append(items, buildAdminOAuthClientWebhookItem(row))
		}
		resp := adminOAuthClientWebhookListResponse{
			GeneratedAt: adminNowUTC(),
			ClientID:    clientID,
			Total:       len(items),
			Items:       items,
		}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookList, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{"total": resp.Total})
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleCreateHydraOAuthClientWebhook(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		if clientID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookCreate, adminAuditTargetTypeOAuthClient, "", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingClientID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id is required")
		}

		if _, err := oauth2Module.GetHydraOAuthClient(c.Context(), clientID); err != nil {
			if oauth2Module.IsHydraNotFoundError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookCreate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClientNotFound, map[string]any{"hydraMode": true}))
				return harukiAPIHelper.ErrorNotFound(c, "oauth client not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookCreate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryClientFailed, map[string]any{"hydraMode": true}))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client")
		}

		var payload adminOAuthClientWebhookPayload
		if err := c.Bind().Body(&payload); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookCreate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}
		callbackURL, err := sanitizeAdminOAuthWebhookCallbackURL(payload.CallbackURL)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookCreate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidCallbackURL, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid callbackUrl")
		}
		webhookID, err := generateAdminOAuthClientWebhookID()
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookCreate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonGenerateWebhookIDFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to create oauth client webhook")
		}

		create := apiHelper.DBManager.DB.OAuth2ClientWebhookEndpoint.Create().
			SetID(webhookID).
			SetClientID(clientID).
			SetCallbackURL(callbackURL)
		if payload.Enabled == nil {
			create.SetEnabled(true)
		} else {
			create.SetEnabled(*payload.Enabled)
		}
		if bearer := sanitizeAdminOAuthWebhookBearer(payload.Bearer); bearer != nil {
			create.SetBearer(*bearer)
		}

		created, err := create.Save(c.Context())
		if err != nil {
			if postgresql.IsConstraintError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookCreate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonWebhookConflict, nil))
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "oauth client webhook conflict", nil)
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookCreate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonCreateWebhookFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to create oauth client webhook")
		}

		resp := buildAdminOAuthClientWebhookMutationResponse(created)
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookCreate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{"webhookID": created.ID, "enabled": created.Enabled, "bearerSet": resp.Webhook.BearerSet})
		return harukiAPIHelper.SuccessResponse(c, "oauth client webhook created", &resp)
	}
}

func handleUpdateHydraOAuthClientWebhook(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		webhookID := strings.TrimSpace(c.Params("webhook_id"))
		if clientID == "" || webhookID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingClientID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id and webhook_id are required")
		}

		var payload adminOAuthClientWebhookPayload
		if err := c.Bind().Body(&payload); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		current, err := apiHelper.DBManager.DB.OAuth2ClientWebhookEndpoint.Query().
			Where(oauth2clientwebhookendpoint.IDEQ(webhookID), oauth2clientwebhookendpoint.ClientIDEQ(clientID)).
			Only(c.Context())
		if err != nil {
			if postgresql.IsNotFound(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonWebhookNotFound, nil))
				return harukiAPIHelper.ErrorNotFound(c, "oauth client webhook not found")
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonQueryWebhooksFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to query oauth client webhook")
		}

		update := apiHelper.DBManager.DB.OAuth2ClientWebhookEndpoint.UpdateOneID(current.ID)
		changed := false
		if strings.TrimSpace(payload.CallbackURL) != "" {
			callbackURL, err := sanitizeAdminOAuthWebhookCallbackURL(payload.CallbackURL)
			if err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidCallbackURL, nil))
				return harukiAPIHelper.ErrorBadRequest(c, "invalid callbackUrl")
			}
			update.SetCallbackURL(callbackURL)
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
			if bearer := sanitizeAdminOAuthWebhookBearer(payload.Bearer); bearer != nil {
				update.SetBearer(*bearer)
			} else {
				update.ClearBearer()
			}
			changed = true
		}
		if !changed {
			resp := buildAdminOAuthClientWebhookMutationResponse(current)
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{"webhookID": webhookID, "noChange": true})
			return harukiAPIHelper.SuccessResponse(c, "oauth client webhook updated", &resp)
		}

		updated, err := update.Save(c.Context())
		if err != nil {
			if postgresql.IsConstraintError(err) {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonWebhookConflict, nil))
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiber.StatusConflict, "oauth client webhook conflict", nil)
			}
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonUpdateWebhookFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to update oauth client webhook")
		}

		resp := buildAdminOAuthClientWebhookMutationResponse(updated)
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookUpdate, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{"webhookID": webhookID, "enabled": updated.Enabled, "bearerSet": resp.Webhook.BearerSet})
		return harukiAPIHelper.SuccessResponse(c, "oauth client webhook updated", &resp)
	}
}

func handleDeleteHydraOAuthClientWebhook(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID := strings.TrimSpace(c.Params("client_id"))
		webhookID := strings.TrimSpace(c.Params("webhook_id"))
		if clientID == "" || webhookID == "" {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonMissingClientID, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "client_id and webhook_id are required")
		}

		affected, err := apiHelper.DBManager.DB.OAuth2ClientWebhookEndpoint.Delete().
			Where(oauth2clientwebhookendpoint.IDEQ(webhookID), oauth2clientwebhookendpoint.ClientIDEQ(clientID)).
			Exec(c.Context())
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonDeleteWebhookFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to delete oauth client webhook")
		}
		if affected == 0 {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonWebhookNotFound, nil))
			return harukiAPIHelper.ErrorNotFound(c, "oauth client webhook not found")
		}

		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionOAuthClientWebhookDelete, adminAuditTargetTypeOAuthClient, clientID, harukiAPIHelper.SystemLogResultSuccess, map[string]any{"webhookID": webhookID})
		return harukiAPIHelper.SuccessResponse[string](c, "oauth client webhook deleted", nil)
	}
}

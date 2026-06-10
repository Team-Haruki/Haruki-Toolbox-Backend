package adminwebhook

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/webhookendpoint"
	harukiHandler "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/handler"
	"strings"

	sql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v3"
)

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

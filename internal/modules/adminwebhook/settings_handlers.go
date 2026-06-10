package adminwebhook

import (
	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"

	"github.com/gofiber/fiber/v3"
)

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

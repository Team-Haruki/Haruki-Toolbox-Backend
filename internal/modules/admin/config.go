package admin

import (
	adminCoreModule "haruki-suite/internal/modules/admincore"
	harukiAPIHelper "haruki-suite/utils/api"
	harukiRedis "haruki-suite/utils/database/redis"
	"strings"

	"github.com/gofiber/fiber/v3"
)

type publicAPIKeysPayload struct {
	PublicAPIAllowedKeys []string `json:"publicApiAllowedKeys"`
}

type publicAPIKeysResponse struct {
	PublicAPIAllowedKeys []string `json:"publicApiAllowedKeys"`
}

type runtimeConfigPayload struct {
	PublicAPIAllowedKeys *([]string) `json:"publicApiAllowedKeys,omitempty"`
	PrivateAPIToken      *string     `json:"privateApiToken,omitempty"`
	PrivateAPIUserAgent  *string     `json:"privateApiUserAgent,omitempty"`
	HarukiProxyUserAgent *string     `json:"harukiProxyUserAgent,omitempty"`
	HarukiProxyVersion   *string     `json:"harukiProxyVersion,omitempty"`
	HarukiProxySecret    *string     `json:"harukiProxySecret,omitempty"`
	HarukiProxyUnpackKey *string     `json:"harukiProxyUnpackKey,omitempty"`
	WebhookJWTSecret     *string     `json:"webhookJwtSecret,omitempty"`
	WebhookEnabled       *bool       `json:"webhookEnabled,omitempty"`
}

type runtimeConfigResponse struct {
	PublicAPIAllowedKeys []string `json:"publicApiAllowedKeys"`

	PrivateAPITokenConfigured      bool   `json:"privateApiTokenConfigured"`
	PrivateAPIUserAgent            string `json:"privateApiUserAgent"`
	HarukiProxyUserAgent           string `json:"harukiProxyUserAgent"`
	HarukiProxyVersion             string `json:"harukiProxyVersion"`
	HarukiProxySecretConfigured    bool   `json:"harukiProxySecretConfigured"`
	HarukiProxyUnpackKeyConfigured bool   `json:"harukiProxyUnpackKeyConfigured"`
	WebhookJWTSecretConfigured     bool   `json:"webhookJwtSecretConfigured"`
	WebhookEnabled                 bool   `json:"webhookEnabled"`
}

func sanitizePublicAPIAllowedKeys(keys []string) ([]string, error) {
	result := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))

	for _, key := range keys {
		normalizedKey := strings.TrimSpace(key)
		if normalizedKey == "" {
			return nil, fiber.NewError(fiber.StatusBadRequest, "publicApiAllowedKeys contains empty value")
		}
		if _, ok := seen[normalizedKey]; ok {
			continue
		}
		seen[normalizedKey] = struct{}{}
		result = append(result, normalizedKey)
	}

	return result, nil
}

func sanitizeOptionalRuntimeSecret(raw *string, fieldName string) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, fieldName+" cannot be empty")
	}
	out := trimmed
	return &out, nil
}

func buildRuntimeConfigResponse(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) runtimeConfigResponse {
	privateAPIToken, privateAPIUserAgent := apiHelper.GetPrivateAPIAuth()
	harukiProxyUserAgent, harukiProxyVersion, harukiProxySecret, harukiProxyUnpackKey := apiHelper.GetHarukiProxyConfig()
	webhookJWTSecret := apiHelper.GetWebhookJWTSecret()

	return runtimeConfigResponse{
		PublicAPIAllowedKeys: append([]string(nil), apiHelper.GetPublicAPIAllowedKeys()...),

		PrivateAPITokenConfigured:      strings.TrimSpace(privateAPIToken) != "",
		PrivateAPIUserAgent:            strings.TrimSpace(privateAPIUserAgent),
		HarukiProxyUserAgent:           strings.TrimSpace(harukiProxyUserAgent),
		HarukiProxyVersion:             strings.TrimSpace(harukiProxyVersion),
		HarukiProxySecretConfigured:    strings.TrimSpace(harukiProxySecret) != "",
		HarukiProxyUnpackKeyConfigured: strings.TrimSpace(harukiProxyUnpackKey) != "",
		WebhookJWTSecretConfigured:     strings.TrimSpace(webhookJWTSecret) != "",
		WebhookEnabled:                 apiHelper.GetWebhookEnabled(),
	}
}

func clearPublicAccessCache(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, c fiber.Ctx) error {
	if apiHelper == nil || apiHelper.DBManager == nil || apiHelper.DBManager.Redis == nil {
		return nil
	}
	return apiHelper.DBManager.Redis.ClearNamespace(c.Context(), harukiRedis.PublicAccessNamespace())
}

func handleGetPublicAPIAllowedKeys(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		keys := apiHelper.GetPublicAPIAllowedKeys()
		resp := publicAPIKeysResponse{PublicAPIAllowedKeys: keys}
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleUpdatePublicAPIAllowedKeys(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		var payload publicAPIKeysPayload
		if err := c.Bind().Body(&payload); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionConfigPublicAPIKeysUpdate, adminAuditTargetTypeConfig, "public_api_allowed_keys", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		sanitizedKeys, err := sanitizePublicAPIAllowedKeys(payload.PublicAPIAllowedKeys)
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionConfigPublicAPIKeysUpdate, adminAuditTargetTypeConfig, "public_api_allowed_keys", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidPublicApiKeys, nil))
			return respondFiberOrBadRequest(c, err, "invalid public api keys")
		}

		if err := clearPublicAccessCache(apiHelper, c); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionConfigPublicAPIKeysUpdate, adminAuditTargetTypeConfig, "public_api_allowed_keys", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClearPublicCacheFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to clear public api cache")
		}
		if err := apiHelper.UpdateRuntimeConfig(harukiAPIHelper.RuntimeConfigUpdate{
			PublicAPIAllowedKeys: &sanitizedKeys,
		}); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionConfigPublicAPIKeysUpdate, adminAuditTargetTypeConfig, "public_api_allowed_keys", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPersistRuntimeConfigFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to persist runtime config")
		}

		resp := publicAPIKeysResponse{PublicAPIAllowedKeys: sanitizedKeys}
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionConfigPublicAPIKeysUpdate, adminAuditTargetTypeConfig, "public_api_allowed_keys", harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"keyCount": len(sanitizedKeys),
		})
		return harukiAPIHelper.SuccessResponse(c, "public api keys updated", &resp)
	}
}

func handleGetRuntimeConfig(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		resp := buildRuntimeConfigResponse(apiHelper)
		return harukiAPIHelper.SuccessResponse(c, "success", &resp)
	}
}

func handleUpdateRuntimeConfig(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		var payload runtimeConfigPayload
		if err := c.Bind().Body(&payload); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionConfigRuntimeUpdate, adminAuditTargetTypeConfig, "runtime", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidRequestPayload, nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		update := harukiAPIHelper.RuntimeConfigUpdate{}
		if payload.PublicAPIAllowedKeys != nil {
			sanitizedKeys, err := sanitizePublicAPIAllowedKeys(*payload.PublicAPIAllowedKeys)
			if err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionConfigRuntimeUpdate, adminAuditTargetTypeConfig, "runtime", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidPublicApiKeys, nil))
				return respondFiberOrBadRequest(c, err, "invalid public api keys")
			}
			update.PublicAPIAllowedKeys = &sanitizedKeys
		}

		privateAPIToken, err := sanitizeOptionalRuntimeSecret(payload.PrivateAPIToken, "privateApiToken")
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionConfigRuntimeUpdate, adminAuditTargetTypeConfig, "runtime", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidPrivateApiToken, nil))
			return respondFiberOrBadRequest(c, err, "invalid privateApiToken")
		}
		harukiProxySecret, err := sanitizeOptionalRuntimeSecret(payload.HarukiProxySecret, "harukiProxySecret")
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionConfigRuntimeUpdate, adminAuditTargetTypeConfig, "runtime", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidHarukiProxySecret, nil))
			return respondFiberOrBadRequest(c, err, "invalid harukiProxySecret")
		}
		harukiProxyUnpackKey, err := sanitizeOptionalRuntimeSecret(payload.HarukiProxyUnpackKey, "harukiProxyUnpackKey")
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionConfigRuntimeUpdate, adminAuditTargetTypeConfig, "runtime", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidHarukiProxyUnpackKey, nil))
			return respondFiberOrBadRequest(c, err, "invalid harukiProxyUnpackKey")
		}
		webhookJWTSecret, err := sanitizeOptionalRuntimeSecret(payload.WebhookJWTSecret, "webhookJwtSecret")
		if err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionConfigRuntimeUpdate, adminAuditTargetTypeConfig, "runtime", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonInvalidWebhookJwtSecret, nil))
			return respondFiberOrBadRequest(c, err, "invalid webhookJwtSecret")
		}

		if privateAPIToken != nil {
			update.PrivateAPIToken = privateAPIToken
		}
		if payload.PrivateAPIUserAgent != nil {
			privateAPIUserAgent := strings.TrimSpace(*payload.PrivateAPIUserAgent)
			update.PrivateAPIUserAgent = &privateAPIUserAgent
		}
		if payload.HarukiProxyUserAgent != nil {
			harukiProxyUserAgent := strings.TrimSpace(*payload.HarukiProxyUserAgent)
			update.HarukiProxyUserAgent = &harukiProxyUserAgent
		}
		if payload.HarukiProxyVersion != nil {
			harukiProxyVersion := strings.TrimSpace(*payload.HarukiProxyVersion)
			update.HarukiProxyVersion = &harukiProxyVersion
		}
		if harukiProxySecret != nil {
			update.HarukiProxySecret = harukiProxySecret
		}
		if harukiProxyUnpackKey != nil {
			update.HarukiProxyUnpackKey = harukiProxyUnpackKey
		}
		if webhookJWTSecret != nil {
			update.WebhookJWTSecret = webhookJWTSecret
		}
		if payload.WebhookEnabled != nil {
			update.WebhookEnabled = payload.WebhookEnabled
		}

		if update.PublicAPIAllowedKeys != nil {
			if err := clearPublicAccessCache(apiHelper, c); err != nil {
				adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionConfigRuntimeUpdate, adminAuditTargetTypeConfig, "runtime", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonClearPublicCacheFailed, nil))
				return harukiAPIHelper.ErrorInternal(c, "failed to clear public api cache")
			}
		}
		if err := apiHelper.UpdateRuntimeConfig(update); err != nil {
			adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionConfigRuntimeUpdate, adminAuditTargetTypeConfig, "runtime", harukiAPIHelper.SystemLogResultFailure, adminCoreModule.AdminFailureMetadata(adminFailureReasonPersistRuntimeConfigFailed, nil))
			return harukiAPIHelper.ErrorInternal(c, "failed to persist runtime config")
		}

		resp := buildRuntimeConfigResponse(apiHelper)
		adminCoreModule.WriteAdminAuditLog(c, apiHelper, adminAuditActionConfigRuntimeUpdate, adminAuditTargetTypeConfig, "runtime", harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"updatedPublicAPIKeys": payload.PublicAPIAllowedKeys != nil,
			"updatedPrivateToken":  privateAPIToken != nil,
			"updatedWebhookSecret": webhookJWTSecret != nil,
			"updatedWebhookFlag":   payload.WebhookEnabled != nil,
		})
		return harukiAPIHelper.SuccessResponse(c, "runtime config updated", &resp)
	}
}

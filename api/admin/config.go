package admin

import (
	harukiConfig "haruki-suite/config"
	harukiAPIHelper "haruki-suite/utils/api"
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
	return runtimeConfigResponse{
		PublicAPIAllowedKeys: append([]string(nil), apiHelper.GetPublicAPIAllowedKeys()...),

		PrivateAPITokenConfigured:      strings.TrimSpace(apiHelper.PrivateAPIToken) != "",
		PrivateAPIUserAgent:            strings.TrimSpace(apiHelper.PrivateAPIUserAgent),
		HarukiProxyUserAgent:           strings.TrimSpace(apiHelper.HarukiProxyUserAgent),
		HarukiProxyVersion:             strings.TrimSpace(apiHelper.HarukiProxyVersion),
		HarukiProxySecretConfigured:    strings.TrimSpace(apiHelper.HarukiProxySecret) != "",
		HarukiProxyUnpackKeyConfigured: strings.TrimSpace(apiHelper.HarukiProxyUnpackKey) != "",
		WebhookJWTSecretConfigured:     strings.TrimSpace(apiHelper.WebhookJWTSecret) != "",
	}
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
			writeAdminAuditLog(c, apiHelper, "admin.config.public_api_keys.update", "config", "public_api_allowed_keys", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		sanitizedKeys, err := sanitizePublicAPIAllowedKeys(payload.PublicAPIAllowedKeys)
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.config.public_api_keys.update", "config", "public_api_allowed_keys", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_public_api_keys", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid public api keys")
		}

		harukiConfig.Cfg.Others.PublicAPIAllowedKeys = append([]string(nil), sanitizedKeys...)
		apiHelper.SetPublicAPIAllowedKeys(sanitizedKeys)

		resp := publicAPIKeysResponse{PublicAPIAllowedKeys: sanitizedKeys}
		writeAdminAuditLog(c, apiHelper, "admin.config.public_api_keys.update", "config", "public_api_allowed_keys", harukiAPIHelper.SystemLogResultSuccess, map[string]any{
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
			writeAdminAuditLog(c, apiHelper, "admin.config.runtime.update", "config", "runtime", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_request_payload", nil))
			return harukiAPIHelper.ErrorBadRequest(c, "invalid request payload")
		}

		if payload.PublicAPIAllowedKeys != nil {
			sanitizedKeys, err := sanitizePublicAPIAllowedKeys(*payload.PublicAPIAllowedKeys)
			if err != nil {
				writeAdminAuditLog(c, apiHelper, "admin.config.runtime.update", "config", "runtime", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_public_api_keys", nil))
				if fiberErr, ok := err.(*fiber.Error); ok {
					return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
				}
				return harukiAPIHelper.ErrorBadRequest(c, "invalid public api keys")
			}
			apiHelper.SetPublicAPIAllowedKeys(sanitizedKeys)
			harukiConfig.Cfg.Others.PublicAPIAllowedKeys = append([]string(nil), sanitizedKeys...)
		}

		privateAPIToken, err := sanitizeOptionalRuntimeSecret(payload.PrivateAPIToken, "privateApiToken")
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.config.runtime.update", "config", "runtime", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_private_api_token", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid privateApiToken")
		}
		harukiProxySecret, err := sanitizeOptionalRuntimeSecret(payload.HarukiProxySecret, "harukiProxySecret")
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.config.runtime.update", "config", "runtime", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_haruki_proxy_secret", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid harukiProxySecret")
		}
		harukiProxyUnpackKey, err := sanitizeOptionalRuntimeSecret(payload.HarukiProxyUnpackKey, "harukiProxyUnpackKey")
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.config.runtime.update", "config", "runtime", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_haruki_proxy_unpack_key", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid harukiProxyUnpackKey")
		}
		webhookJWTSecret, err := sanitizeOptionalRuntimeSecret(payload.WebhookJWTSecret, "webhookJwtSecret")
		if err != nil {
			writeAdminAuditLog(c, apiHelper, "admin.config.runtime.update", "config", "runtime", harukiAPIHelper.SystemLogResultFailure, adminFailureMetadata("invalid_webhook_jwt_secret", nil))
			if fiberErr, ok := err.(*fiber.Error); ok {
				return harukiAPIHelper.UpdatedDataResponse[string](c, fiberErr.Code, fiberErr.Message, nil)
			}
			return harukiAPIHelper.ErrorBadRequest(c, "invalid webhookJwtSecret")
		}

		if privateAPIToken != nil {
			apiHelper.PrivateAPIToken = *privateAPIToken
			harukiConfig.Cfg.MongoDB.PrivateApiSecret = *privateAPIToken
		}
		if payload.PrivateAPIUserAgent != nil {
			apiHelper.PrivateAPIUserAgent = strings.TrimSpace(*payload.PrivateAPIUserAgent)
			harukiConfig.Cfg.MongoDB.PrivateApiUserAgent = strings.TrimSpace(*payload.PrivateAPIUserAgent)
		}
		if payload.HarukiProxyUserAgent != nil {
			apiHelper.HarukiProxyUserAgent = strings.TrimSpace(*payload.HarukiProxyUserAgent)
			harukiConfig.Cfg.HarukiProxy.UserAgent = strings.TrimSpace(*payload.HarukiProxyUserAgent)
		}
		if payload.HarukiProxyVersion != nil {
			apiHelper.HarukiProxyVersion = strings.TrimSpace(*payload.HarukiProxyVersion)
			harukiConfig.Cfg.HarukiProxy.Version = strings.TrimSpace(*payload.HarukiProxyVersion)
		}
		if harukiProxySecret != nil {
			apiHelper.HarukiProxySecret = *harukiProxySecret
			harukiConfig.Cfg.HarukiProxy.Secret = *harukiProxySecret
		}
		if harukiProxyUnpackKey != nil {
			apiHelper.HarukiProxyUnpackKey = *harukiProxyUnpackKey
			harukiConfig.Cfg.HarukiProxy.UnpackKey = *harukiProxyUnpackKey
		}
		if webhookJWTSecret != nil {
			apiHelper.WebhookJWTSecret = *webhookJWTSecret
			harukiConfig.Cfg.Webhook.JWTSecret = *webhookJWTSecret
		}

		resp := buildRuntimeConfigResponse(apiHelper)
		writeAdminAuditLog(c, apiHelper, "admin.config.runtime.update", "config", "runtime", harukiAPIHelper.SystemLogResultSuccess, map[string]any{
			"updatedPublicAPIKeys": payload.PublicAPIAllowedKeys != nil,
			"updatedPrivateToken":  privateAPIToken != nil,
			"updatedWebhookSecret": webhookJWTSecret != nil,
		})
		return harukiAPIHelper.SuccessResponse(c, "runtime config updated", &resp)
	}
}

package api

import (
	"context"

	platformRuntimeConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/runtimeconfig"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/sekaiapi"
	smtp2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/smtp"
	"sync"

	"github.com/gofiber/fiber/v3"
	"github.com/redis/go-redis/v9"
)

type RuntimeConfigUpdate = platformRuntimeConfig.Update

type HarukiToolboxRouterHelpers struct {
	Router         fiber.Router
	DBManager      *database.HarukiToolboxDBManager
	SMTPClient     *smtp2.HarukiSMTPClient
	SessionHandler *SessionHandler
	SekaiAPIClient *sekaiapi.HarukiSekaiAPIClient
	RuntimeConfig  *platformRuntimeConfig.Service

	// Deprecated compatibility fields. New modules should depend on
	// RuntimeConfig (or a narrower interface) instead of this service locator.
	PublicAPIAllowedKeys   []string
	PrivateAPIToken        string
	PrivateAPIUserAgent    string
	HarukiProxyUserAgent   string
	HarukiProxyVersion     string
	HarukiProxySecret      string
	HarukiProxyUnpackKey   string
	WebhookJWTSecret       string
	WebhookEnabled         *bool
	BotRegistrationEnabled bool
	BotCredentialSignToken string
	publicAPIKeysMu        sync.RWMutex
	runtimeConfigMu        sync.RWMutex
}

func NewHarukiToolboxRouterHelpers(
	router fiber.Router,
	dbManager *database.HarukiToolboxDBManager,
	smtpClient *smtp2.HarukiSMTPClient,
	sessionHandler *SessionHandler,
	sekaiAPIClient *sekaiapi.HarukiSekaiAPIClient,
	runtimeConfig *platformRuntimeConfig.Service,
) *HarukiToolboxRouterHelpers {
	helper := &HarukiToolboxRouterHelpers{
		Router:         router,
		DBManager:      dbManager,
		SMTPClient:     smtpClient,
		SessionHandler: sessionHandler,
		SekaiAPIClient: sekaiAPIClient,
		RuntimeConfig:  runtimeConfig,
	}
	if runtimeConfig != nil {
		snapshot, _ := runtimeConfig.Current(context.Background())
		helper.applyLegacyRuntimeConfigSnapshot(snapshot)
	}
	return helper
}

func (h *HarukiToolboxRouterHelpers) legacyRuntimeConfigSnapshot() platformRuntimeConfig.Snapshot {
	if h == nil {
		return platformRuntimeConfig.Snapshot{}
	}
	publicAPIAllowedKeys := func() []string {
		h.publicAPIKeysMu.RLock()
		defer h.publicAPIKeysMu.RUnlock()
		return append([]string(nil), h.PublicAPIAllowedKeys...)
	}()

	h.runtimeConfigMu.RLock()
	defer h.runtimeConfigMu.RUnlock()
	var webhookEnabled *bool
	if h.WebhookEnabled != nil {
		value := *h.WebhookEnabled
		webhookEnabled = &value
	}
	return platformRuntimeConfig.Snapshot{
		PublicAPIAllowedKeys: publicAPIAllowedKeys,
		PrivateAPIToken:      h.PrivateAPIToken,
		PrivateAPIUserAgent:  h.PrivateAPIUserAgent,
		HarukiProxyUserAgent: h.HarukiProxyUserAgent,
		HarukiProxyVersion:   h.HarukiProxyVersion,
		HarukiProxySecret:    h.HarukiProxySecret,
		HarukiProxyUnpackKey: h.HarukiProxyUnpackKey,
		WebhookJWTSecret:     h.WebhookJWTSecret,
		WebhookEnabled:       webhookEnabled,
	}
}

func (h *HarukiToolboxRouterHelpers) applyLegacyRuntimeConfigSnapshot(snapshot platformRuntimeConfig.Snapshot) {
	if h == nil {
		return
	}
	h.runtimeConfigMu.Lock()
	h.PrivateAPIToken = snapshot.PrivateAPIToken
	h.PrivateAPIUserAgent = snapshot.PrivateAPIUserAgent
	h.HarukiProxyUserAgent = snapshot.HarukiProxyUserAgent
	h.HarukiProxyVersion = snapshot.HarukiProxyVersion
	h.HarukiProxySecret = snapshot.HarukiProxySecret
	h.HarukiProxyUnpackKey = snapshot.HarukiProxyUnpackKey
	h.WebhookJWTSecret = snapshot.WebhookJWTSecret
	if snapshot.WebhookEnabled == nil {
		h.WebhookEnabled = nil
	} else {
		webhookEnabled := *snapshot.WebhookEnabled
		h.WebhookEnabled = &webhookEnabled
	}
	h.runtimeConfigMu.Unlock()

	h.publicAPIKeysMu.Lock()
	h.PublicAPIAllowedKeys = append([]string(nil), snapshot.PublicAPIAllowedKeys...)
	h.publicAPIKeysMu.Unlock()
}

func (h *HarukiToolboxRouterHelpers) runtimeConfigService() *platformRuntimeConfig.Service {
	if h == nil {
		return nil
	}
	h.runtimeConfigMu.RLock()
	service := h.RuntimeConfig
	h.runtimeConfigMu.RUnlock()
	if service != nil {
		return service
	}

	var runtimeStore platformRuntimeConfig.Store
	if h.DBManager != nil {
		runtimeStore = platformRuntimeConfig.NewRedisStore(h.DBManager.Redis)
	}
	// Do not retain this compatibility service: callers that still initialize
	// the deprecated public fields directly must see subsequent direct changes.
	return platformRuntimeConfig.New(h.legacyRuntimeConfigSnapshot(), runtimeStore)
}

func (h *HarukiToolboxRouterHelpers) currentRuntimeConfigSnapshot() platformRuntimeConfig.Snapshot {
	service := h.runtimeConfigService()
	if service == nil {
		return platformRuntimeConfig.Snapshot{}
	}
	snapshot, _ := service.Current(context.Background())
	h.applyLegacyRuntimeConfigSnapshot(snapshot)
	return snapshot
}

func (h *HarukiToolboxRouterHelpers) UpdateRuntimeConfig(update RuntimeConfigUpdate) error {
	if h == nil {
		return nil
	}
	service := h.runtimeConfigService()
	if service == nil {
		return nil
	}
	if err := service.Update(context.Background(), update); err != nil {
		return err
	}
	snapshot, _ := service.Current(context.Background())
	h.applyLegacyRuntimeConfigSnapshot(snapshot)
	return nil
}

func (h *HarukiToolboxRouterHelpers) GetPublicAPIAllowedKeys() []string {
	return h.currentRuntimeConfigSnapshot().PublicAPIAllowedKeys
}

func (h *HarukiToolboxRouterHelpers) SetPublicAPIAllowedKeys(keys []string) {
	keysCopy := append([]string(nil), keys...)
	_ = h.UpdateRuntimeConfig(RuntimeConfigUpdate{
		PublicAPIAllowedKeys: &keysCopy,
	})
}

func (h *HarukiToolboxRouterHelpers) GetPrivateAPIAuth() (string, string) {
	snapshot := h.currentRuntimeConfigSnapshot()
	return snapshot.PrivateAPIToken, snapshot.PrivateAPIUserAgent
}

func (h *HarukiToolboxRouterHelpers) SetPrivateAPIToken(token string) {
	_ = h.UpdateRuntimeConfig(RuntimeConfigUpdate{PrivateAPIToken: &token})
}

func (h *HarukiToolboxRouterHelpers) SetPrivateAPIUserAgent(userAgent string) {
	_ = h.UpdateRuntimeConfig(RuntimeConfigUpdate{PrivateAPIUserAgent: &userAgent})
}

func (h *HarukiToolboxRouterHelpers) GetHarukiProxyConfig() (string, string, string, string) {
	snapshot := h.currentRuntimeConfigSnapshot()
	return snapshot.HarukiProxyUserAgent, snapshot.HarukiProxyVersion, snapshot.HarukiProxySecret, snapshot.HarukiProxyUnpackKey
}

func (h *HarukiToolboxRouterHelpers) SetHarukiProxyUserAgent(userAgent string) {
	_ = h.UpdateRuntimeConfig(RuntimeConfigUpdate{HarukiProxyUserAgent: &userAgent})
}

func (h *HarukiToolboxRouterHelpers) SetHarukiProxyVersion(version string) {
	_ = h.UpdateRuntimeConfig(RuntimeConfigUpdate{HarukiProxyVersion: &version})
}

func (h *HarukiToolboxRouterHelpers) SetHarukiProxySecret(secret string) {
	_ = h.UpdateRuntimeConfig(RuntimeConfigUpdate{HarukiProxySecret: &secret})
}

func (h *HarukiToolboxRouterHelpers) SetHarukiProxyUnpackKey(unpackKey string) {
	_ = h.UpdateRuntimeConfig(RuntimeConfigUpdate{HarukiProxyUnpackKey: &unpackKey})
}

func (h *HarukiToolboxRouterHelpers) GetWebhookJWTSecret() string {
	return h.currentRuntimeConfigSnapshot().WebhookJWTSecret
}

func (h *HarukiToolboxRouterHelpers) SetWebhookJWTSecret(secret string) {
	_ = h.UpdateRuntimeConfig(RuntimeConfigUpdate{WebhookJWTSecret: &secret})
}

func (h *HarukiToolboxRouterHelpers) GetWebhookEnabled() bool {
	if h == nil {
		return true
	}
	snapshot := h.currentRuntimeConfigSnapshot()
	if snapshot.WebhookEnabled == nil {
		return true
	}
	return *snapshot.WebhookEnabled
}

func (h *HarukiToolboxRouterHelpers) SetWebhookEnabled(enabled bool) {
	_ = h.UpdateRuntimeConfig(RuntimeConfigUpdate{WebhookEnabled: &enabled})
}

func (h *HarukiToolboxRouterHelpers) RedisClient() *redis.Client {
	if h == nil || h.DBManager == nil || h.DBManager.Redis == nil {
		return nil
	}
	return h.DBManager.Redis.Redis
}

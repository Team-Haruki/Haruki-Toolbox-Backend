package api

import (
	"context"
	"haruki-suite/utils/database"
	harukiRedis "haruki-suite/utils/database/redis"
	"haruki-suite/utils/sekaiapi"
	smtp2 "haruki-suite/utils/smtp"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/redis/go-redis/v9"
)

const runtimeConfigStoreTimeout = 500 * time.Millisecond

var runtimeConfigStoreUpdateMu sync.Mutex

type RuntimeConfigUpdate struct {
	PublicAPIAllowedKeys *[]string
	PrivateAPIToken      *string
	PrivateAPIUserAgent  *string
	HarukiProxyUserAgent *string
	HarukiProxyVersion   *string
	HarukiProxySecret    *string
	HarukiProxyUnpackKey *string
	WebhookJWTSecret     *string
	WebhookEnabled       *bool
}

type runtimeConfigSnapshot struct {
	PublicAPIAllowedKeys []string `json:"publicApiAllowedKeys"`
	PrivateAPIToken      string   `json:"privateApiToken"`
	PrivateAPIUserAgent  string   `json:"privateApiUserAgent"`
	HarukiProxyUserAgent string   `json:"harukiProxyUserAgent"`
	HarukiProxyVersion   string   `json:"harukiProxyVersion"`
	HarukiProxySecret    string   `json:"harukiProxySecret"`
	HarukiProxyUnpackKey string   `json:"harukiProxyUnpackKey"`
	WebhookJWTSecret     string   `json:"webhookJwtSecret"`
	WebhookEnabled       *bool    `json:"webhookEnabled,omitempty"`
}

type HarukiToolboxRouterHelpers struct {
	Router                 fiber.Router
	DBManager              *database.HarukiToolboxDBManager
	SMTPClient             *smtp2.HarukiSMTPClient
	SessionHandler         *SessionHandler
	SekaiAPIClient         *sekaiapi.HarukiSekaiAPIClient
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
	publicAPIAllowedKeys []string,
	privateAPIToken string,
	privateAPIUserAgent string,
	harukiProxyUserAgent string,
	harukiProxyVersion string,
	harukiProxySecret string,
	HarukiProxyUnpackKey string,
	webhookJWTSecret string,
	webhookEnabled bool,
) *HarukiToolboxRouterHelpers {
	copiedPublicAPIAllowedKeys := append([]string(nil), publicAPIAllowedKeys...)
	webhookEnabledCopy := webhookEnabled

	return &HarukiToolboxRouterHelpers{
		Router:               router,
		DBManager:            dbManager,
		SMTPClient:           smtpClient,
		SessionHandler:       sessionHandler,
		SekaiAPIClient:       sekaiAPIClient,
		PublicAPIAllowedKeys: copiedPublicAPIAllowedKeys,
		PrivateAPIToken:      privateAPIToken,
		PrivateAPIUserAgent:  privateAPIUserAgent,
		HarukiProxyUserAgent: harukiProxyUserAgent,
		HarukiProxyVersion:   harukiProxyVersion,
		HarukiProxySecret:    harukiProxySecret,
		HarukiProxyUnpackKey: HarukiProxyUnpackKey,
		WebhookJWTSecret:     webhookJWTSecret,
		WebhookEnabled:       &webhookEnabledCopy,
	}
}

func NewHarukiToolboxDBHelpers(
	router fiber.Router,
	dbManager *database.HarukiToolboxDBManager,
	smtpClient *smtp2.HarukiSMTPClient,
	sessionHandler *SessionHandler,
	sekaiAPIClient *sekaiapi.HarukiSekaiAPIClient,
	publicAPIAllowedKeys []string,
	privateAPIToken string,
	privateAPIUserAgent string,
	harukiProxyUserAgent string,
	harukiProxyVersion string,
	harukiProxySecret string,
	HarukiProxyUnpackKey string,
	webhookJWTSecret string,
	webhookEnabled bool,
) *HarukiToolboxRouterHelpers {
	return NewHarukiToolboxRouterHelpers(
		router,
		dbManager,
		smtpClient,
		sessionHandler,
		sekaiAPIClient,
		publicAPIAllowedKeys,
		privateAPIToken,
		privateAPIUserAgent,
		harukiProxyUserAgent,
		harukiProxyVersion,
		harukiProxySecret,
		HarukiProxyUnpackKey,
		webhookJWTSecret,
		webhookEnabled,
	)
}

func (h *HarukiToolboxRouterHelpers) runtimeConfigStoreKey() string {
	return harukiRedis.BuildRuntimeConfigKey()
}

func (h *HarukiToolboxRouterHelpers) currentRuntimeConfigSnapshot() runtimeConfigSnapshot {
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
	return runtimeConfigSnapshot{
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

func (h *HarukiToolboxRouterHelpers) applyRuntimeConfigSnapshot(snapshot runtimeConfigSnapshot) {
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

func (h *HarukiToolboxRouterHelpers) loadRuntimeConfigSnapshotFromStore(ctx context.Context) (*runtimeConfigSnapshot, bool, error) {
	if h == nil || h.DBManager == nil || h.DBManager.Redis == nil {
		return nil, false, nil
	}
	var snapshot runtimeConfigSnapshot
	found, err := h.DBManager.Redis.GetCache(ctx, h.runtimeConfigStoreKey(), &snapshot)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}
	snapshot.PublicAPIAllowedKeys = append([]string(nil), snapshot.PublicAPIAllowedKeys...)
	return &snapshot, true, nil
}

func (h *HarukiToolboxRouterHelpers) syncRuntimeConfigFromStore() {
	if h == nil || h.DBManager == nil || h.DBManager.Redis == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), runtimeConfigStoreTimeout)
	defer cancel()
	snapshot, found, err := h.loadRuntimeConfigSnapshotFromStore(ctx)
	if err != nil || !found || snapshot == nil {
		return
	}
	h.applyRuntimeConfigSnapshot(*snapshot)
}

func (h *HarukiToolboxRouterHelpers) persistRuntimeConfigSnapshot(ctx context.Context, snapshot runtimeConfigSnapshot) error {
	if h == nil || h.DBManager == nil || h.DBManager.Redis == nil {
		return nil
	}
	snapshot.PublicAPIAllowedKeys = append([]string(nil), snapshot.PublicAPIAllowedKeys...)
	return h.DBManager.Redis.SetCache(ctx, h.runtimeConfigStoreKey(), snapshot, 0)
}

func (h *HarukiToolboxRouterHelpers) UpdateRuntimeConfig(update RuntimeConfigUpdate) error {
	if h == nil {
		return nil
	}
	runtimeConfigStoreUpdateMu.Lock()
	defer runtimeConfigStoreUpdateMu.Unlock()

	h.syncRuntimeConfigFromStore()
	snapshot := h.currentRuntimeConfigSnapshot()

	if update.PublicAPIAllowedKeys != nil {
		snapshot.PublicAPIAllowedKeys = append([]string(nil), (*update.PublicAPIAllowedKeys)...)
	}
	if update.PrivateAPIToken != nil {
		snapshot.PrivateAPIToken = *update.PrivateAPIToken
	}
	if update.PrivateAPIUserAgent != nil {
		snapshot.PrivateAPIUserAgent = *update.PrivateAPIUserAgent
	}
	if update.HarukiProxyUserAgent != nil {
		snapshot.HarukiProxyUserAgent = *update.HarukiProxyUserAgent
	}
	if update.HarukiProxyVersion != nil {
		snapshot.HarukiProxyVersion = *update.HarukiProxyVersion
	}
	if update.HarukiProxySecret != nil {
		snapshot.HarukiProxySecret = *update.HarukiProxySecret
	}
	if update.HarukiProxyUnpackKey != nil {
		snapshot.HarukiProxyUnpackKey = *update.HarukiProxyUnpackKey
	}
	if update.WebhookJWTSecret != nil {
		snapshot.WebhookJWTSecret = *update.WebhookJWTSecret
	}
	if update.WebhookEnabled != nil {
		webhookEnabled := *update.WebhookEnabled
		snapshot.WebhookEnabled = &webhookEnabled
	}

	if h.DBManager != nil && h.DBManager.Redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), runtimeConfigStoreTimeout)
		defer cancel()
		if err := h.persistRuntimeConfigSnapshot(ctx, snapshot); err != nil {
			return err
		}
	}
	h.applyRuntimeConfigSnapshot(snapshot)
	return nil
}

func (h *HarukiToolboxRouterHelpers) GetPublicAPIAllowedKeys() []string {
	h.syncRuntimeConfigFromStore()
	h.publicAPIKeysMu.RLock()
	defer h.publicAPIKeysMu.RUnlock()
	return append([]string(nil), h.PublicAPIAllowedKeys...)
}

func (h *HarukiToolboxRouterHelpers) SetPublicAPIAllowedKeys(keys []string) {
	keysCopy := append([]string(nil), keys...)
	_ = h.UpdateRuntimeConfig(RuntimeConfigUpdate{
		PublicAPIAllowedKeys: &keysCopy,
	})
}

func (h *HarukiToolboxRouterHelpers) GetPrivateAPIAuth() (string, string) {
	h.syncRuntimeConfigFromStore()
	h.runtimeConfigMu.RLock()
	defer h.runtimeConfigMu.RUnlock()
	return h.PrivateAPIToken, h.PrivateAPIUserAgent
}

func (h *HarukiToolboxRouterHelpers) SetPrivateAPIToken(token string) {
	_ = h.UpdateRuntimeConfig(RuntimeConfigUpdate{PrivateAPIToken: &token})
}

func (h *HarukiToolboxRouterHelpers) SetPrivateAPIUserAgent(userAgent string) {
	_ = h.UpdateRuntimeConfig(RuntimeConfigUpdate{PrivateAPIUserAgent: &userAgent})
}

func (h *HarukiToolboxRouterHelpers) GetHarukiProxyConfig() (string, string, string, string) {
	h.syncRuntimeConfigFromStore()
	h.runtimeConfigMu.RLock()
	defer h.runtimeConfigMu.RUnlock()
	return h.HarukiProxyUserAgent, h.HarukiProxyVersion, h.HarukiProxySecret, h.HarukiProxyUnpackKey
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
	h.syncRuntimeConfigFromStore()
	h.runtimeConfigMu.RLock()
	defer h.runtimeConfigMu.RUnlock()
	return h.WebhookJWTSecret
}

func (h *HarukiToolboxRouterHelpers) SetWebhookJWTSecret(secret string) {
	_ = h.UpdateRuntimeConfig(RuntimeConfigUpdate{WebhookJWTSecret: &secret})
}

func (h *HarukiToolboxRouterHelpers) GetWebhookEnabled() bool {
	if h == nil {
		return true
	}
	h.syncRuntimeConfigFromStore()
	h.runtimeConfigMu.RLock()
	defer h.runtimeConfigMu.RUnlock()
	if h.WebhookEnabled == nil {
		return true
	}
	return *h.WebhookEnabled
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

package api

import (
	"haruki-suite/utils/database"
	"haruki-suite/utils/sekaiapi"
	smtp2 "haruki-suite/utils/smtp"
	"sync"

	"github.com/gofiber/fiber/v3"
)

type HarukiToolboxRouterHelpers struct {
	Router               fiber.Router
	DBManager            *database.HarukiToolboxDBManager
	SMTPClient           *smtp2.HarukiSMTPClient
	SessionHandler       *SessionHandler
	SekaiAPIClient       *sekaiapi.HarukiSekaiAPIClient
	PublicAPIAllowedKeys []string
	PrivateAPIToken      string
	PrivateAPIUserAgent  string
	HarukiProxyUserAgent string
	HarukiProxyVersion   string
	HarukiProxySecret    string
	HarukiProxyUnpackKey string
	WebhookJWTSecret     string
	publicAPIKeysMu      sync.RWMutex
	runtimeConfigMu      sync.RWMutex
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
) *HarukiToolboxRouterHelpers {
	copiedPublicAPIAllowedKeys := append([]string(nil), publicAPIAllowedKeys...)

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
	)
}

func (h *HarukiToolboxRouterHelpers) GetPublicAPIAllowedKeys() []string {
	h.publicAPIKeysMu.RLock()
	defer h.publicAPIKeysMu.RUnlock()
	return append([]string(nil), h.PublicAPIAllowedKeys...)
}

func (h *HarukiToolboxRouterHelpers) SetPublicAPIAllowedKeys(keys []string) {
	h.publicAPIKeysMu.Lock()
	defer h.publicAPIKeysMu.Unlock()
	h.PublicAPIAllowedKeys = append([]string(nil), keys...)
}

func (h *HarukiToolboxRouterHelpers) GetPrivateAPIAuth() (string, string) {
	h.runtimeConfigMu.RLock()
	defer h.runtimeConfigMu.RUnlock()
	return h.PrivateAPIToken, h.PrivateAPIUserAgent
}

func (h *HarukiToolboxRouterHelpers) SetPrivateAPIToken(token string) {
	h.runtimeConfigMu.Lock()
	defer h.runtimeConfigMu.Unlock()
	h.PrivateAPIToken = token
}

func (h *HarukiToolboxRouterHelpers) SetPrivateAPIUserAgent(userAgent string) {
	h.runtimeConfigMu.Lock()
	defer h.runtimeConfigMu.Unlock()
	h.PrivateAPIUserAgent = userAgent
}

func (h *HarukiToolboxRouterHelpers) GetHarukiProxyConfig() (string, string, string, string) {
	h.runtimeConfigMu.RLock()
	defer h.runtimeConfigMu.RUnlock()
	return h.HarukiProxyUserAgent, h.HarukiProxyVersion, h.HarukiProxySecret, h.HarukiProxyUnpackKey
}

func (h *HarukiToolboxRouterHelpers) SetHarukiProxyUserAgent(userAgent string) {
	h.runtimeConfigMu.Lock()
	defer h.runtimeConfigMu.Unlock()
	h.HarukiProxyUserAgent = userAgent
}

func (h *HarukiToolboxRouterHelpers) SetHarukiProxyVersion(version string) {
	h.runtimeConfigMu.Lock()
	defer h.runtimeConfigMu.Unlock()
	h.HarukiProxyVersion = version
}

func (h *HarukiToolboxRouterHelpers) SetHarukiProxySecret(secret string) {
	h.runtimeConfigMu.Lock()
	defer h.runtimeConfigMu.Unlock()
	h.HarukiProxySecret = secret
}

func (h *HarukiToolboxRouterHelpers) SetHarukiProxyUnpackKey(unpackKey string) {
	h.runtimeConfigMu.Lock()
	defer h.runtimeConfigMu.Unlock()
	h.HarukiProxyUnpackKey = unpackKey
}

func (h *HarukiToolboxRouterHelpers) GetWebhookJWTSecret() string {
	h.runtimeConfigMu.RLock()
	defer h.runtimeConfigMu.RUnlock()
	return h.WebhookJWTSecret
}

func (h *HarukiToolboxRouterHelpers) SetWebhookJWTSecret(secret string) {
	h.runtimeConfigMu.Lock()
	defer h.runtimeConfigMu.Unlock()
	h.WebhookJWTSecret = secret
}

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

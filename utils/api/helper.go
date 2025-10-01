package api

import (
	"haruki-suite/utils/database"
	"haruki-suite/utils/sekaiapi"
	smtp2 "haruki-suite/utils/smtp"

	"github.com/gofiber/fiber/v2"
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
	WebhookJWTSecret     string
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
	webhookJWTSecret string,
) *HarukiToolboxRouterHelpers {
	return &HarukiToolboxRouterHelpers{
		Router:               router,
		DBManager:            dbManager,
		SMTPClient:           smtpClient,
		SessionHandler:       sessionHandler,
		SekaiAPIClient:       sekaiAPIClient,
		PublicAPIAllowedKeys: publicAPIAllowedKeys,
		PrivateAPIToken:      privateAPIToken,
		PrivateAPIUserAgent:  privateAPIUserAgent,
		HarukiProxyUserAgent: harukiProxyUserAgent,
		HarukiProxyVersion:   harukiProxyVersion,
		HarukiProxySecret:    harukiProxySecret,
		WebhookJWTSecret:     webhookJWTSecret,
	}
}

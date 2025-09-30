package user

import (
	"haruki-suite/utils/database"
	"haruki-suite/utils/sekaiapi"
	smtp2 "haruki-suite/utils/smtp"

	"github.com/gofiber/fiber/v2"
)

type HarukiToolboxUserRouterHelpers struct {
	Router         fiber.Router
	DBManager      *database.HarukiToolboxDBManager
	SMTPClient     *smtp2.HarukiSMTPClient
	SessionHandler *SessionHandler
	SekaiAPIClient *sekaiapi.HarukiSekaiAPIClient
}

func NewHarukiToolboxUserSystemHelpers(
	router fiber.Router,
	dbManager *database.HarukiToolboxDBManager,
	smtpClient *smtp2.HarukiSMTPClient,
	sessionHandler *SessionHandler,
	sekaiAPIClient *sekaiapi.HarukiSekaiAPIClient) *HarukiToolboxUserRouterHelpers {
	return &HarukiToolboxUserRouterHelpers{
		Router:         router,
		DBManager:      dbManager,
		SMTPClient:     smtpClient,
		SessionHandler: sessionHandler,
		SekaiAPIClient: sekaiAPIClient,
	}
}

func RegisterUserSystemRoutes(helper HarukiToolboxUserRouterHelpers) {
	RegisterEmailRoutes(helper)
	RegisterLoginRoutes(helper)
	RegisterAccountRoutes(helper)
	RegisterRegisterRoutes(helper)
	RegisterResetPasswordRoutes(helper)
	RegisterSocialPlatformRoutes(helper)
	RegisterGameAccountBindingRoutes(helper)
	RegisterAuthorizeSocialPlatformRoutes(helper)
}

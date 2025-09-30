package user

import (
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/sekaiapi"
	smtp2 "haruki-suite/utils/smtp"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

type HarukiToolboxUserRouterHelpers struct {
	Router         fiber.Router
	DBClient       *postgresql.Client
	RedisClient    *redis.Client
	SMTPClient     *smtp2.HarukiSMTPClient
	SessionHandler *SessionHandler
	SekaiAPIClient *sekaiapi.HarukiSekaiAPIClient
}

func NewHarukiToolboxUserSystemHelpers(
	router fiber.Router,
	dbClient *postgresql.Client,
	redisClient *redis.Client,
	smtpClient *smtp2.HarukiSMTPClient,
	sessionHandler *SessionHandler,
	sekaiAPIClient *sekaiapi.HarukiSekaiAPIClient) *HarukiToolboxUserRouterHelpers {
	return &HarukiToolboxUserRouterHelpers{
		Router:         router,
		DBClient:       dbClient,
		RedisClient:    redisClient,
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

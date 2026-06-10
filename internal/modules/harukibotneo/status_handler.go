package harukibotneo

import (
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"

	"github.com/gofiber/fiber/v3"
)

func handleGetStatus(apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		resp := registrationStatusResponse{Enabled: apiHelper.BotRegistrationEnabled}
		return harukiAPIHelper.SuccessResponse(c, "ok", &resp)
	}
}

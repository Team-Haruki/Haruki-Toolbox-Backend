package userprivateapi

import (
	"crypto/subtle"
	harukiApiHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func ValidateUserPermission(apiHelper *harukiApiHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		if apiHelper == nil {
			return harukiApiHelper.ErrorInternal(c, "private api is not configured")
		}
		expectedToken, requiredAgentKeyword := apiHelper.GetPrivateAPIAuth()
		if strings.TrimSpace(expectedToken) == "" {
			harukiLogger.Errorf("private api token is not configured")
			return harukiApiHelper.ErrorInternal(c, "private api is not configured")
		}
		authorization := c.Get("Authorization")
		userAgent := c.Get("User-Agent")
		if subtle.ConstantTimeCompare([]byte(authorization), []byte(expectedToken)) != 1 {
			return harukiApiHelper.ErrorUnauthorized(c, "unauthorized token")
		}
		if requiredAgentKeyword != "" && !harukiApiHelper.StringContains(userAgent, requiredAgentKeyword) {
			return harukiApiHelper.ErrorUnauthorized(c, "unauthorized user agent")
		}
		return c.Next()
	}
}

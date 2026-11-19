package userprivateapi

import (
	"crypto/subtle"
	"strings"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiApiHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
)

func ValidateUserPermission(apiHelper *harukiApiHelper.HarukiToolboxRouterHelpers) fiber.Handler {
	return func(c fiber.Ctx) error {
		if apiHelper == nil {
			return harukiApiHelper.ErrorInternal(c, "private api is not configured")
		}
		authorization := c.Get(fiber.HeaderAuthorization)
		if len(authorization) > 4096 || strings.TrimSpace(authorization) == "" {
			return harukiApiHelper.ErrorUnauthorized(c, "unauthorized token")
		}
		snapshot, err := apiHelper.CurrentRuntimeConfigForAuthentication(c.Context())
		if err != nil {
			return harukiApiHelper.ErrorInternal(c, "private api authentication unavailable")
		}
		expectedToken, requiredAgentKeyword := snapshot.PrivateAPIToken, snapshot.PrivateAPIUserAgent
		if strings.TrimSpace(expectedToken) == "" || config.IsExampleCredential(expectedToken) {
			harukiLogger.Errorf("private api token is not configured")
			return harukiApiHelper.ErrorInternal(c, "private api is not configured")
		}
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

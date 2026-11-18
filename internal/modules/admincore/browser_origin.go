package admincore

import (
	"net/url"
	"strings"

	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/gofiber/fiber/v3"
)

// RequireTrustedBrowserOrigin protects cookie-authenticated admin mutations.
// A configured frontend path is irrelevant to its browser origin.
func RequireTrustedBrowserOrigin(frontendURL string) fiber.Handler {
	frontend, err := url.Parse(strings.TrimSpace(frontendURL))
	allowed := ""
	if err == nil && frontend.Hostname() != "" && frontend.User == nil && (frontend.Scheme == "https" || frontend.Scheme == "http") {
		allowed = frontend.Scheme + "://" + strings.ToLower(frontend.Host)
		if frontend.Scheme == "https" && frontend.Port() == "443" {
			allowed = strings.TrimSuffix(allowed, ":443")
		}
		if frontend.Scheme == "http" && frontend.Port() == "80" {
			allowed = strings.TrimSuffix(allowed, ":80")
		}
	}
	return func(c fiber.Ctx) error {
		switch c.Method() {
		case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
			return c.Next()
		}
		origins := c.Request().Header.PeekAll(fiber.HeaderOrigin)
		if allowed == "" || len(origins) != 1 || string(origins[0]) != allowed {
			return api.ErrorForbidden(c, "untrusted admin request origin")
		}
		return c.Next()
	}
}

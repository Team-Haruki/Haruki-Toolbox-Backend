package admingamebindings

import (
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func buildRoleProtectedApp(method string, path string, allowedRoles []string, handler fiber.Handler) *fiber.App {
	withSession := func(c fiber.Ctx) error {
		if userID := strings.TrimSpace(c.Get("X-User-ID")); userID != "" {
			c.Locals("userID", userID)
		}
		return c.Next()
	}

	requireAllowed := func(c fiber.Ctx) error {
		userID, ok := c.Locals("userID").(string)
		if !ok || strings.TrimSpace(userID) == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"status":  fiber.StatusUnauthorized,
				"message": "missing user session",
			})
		}

		role := strings.TrimSpace(c.Get("X-Role"))
		if role == "" {
			role = roleUser
		}
		role = normalizeRole(role)

		for _, allowedRole := range allowedRoles {
			if role == normalizeRole(allowedRole) {
				return c.Next()
			}
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"status":  fiber.StatusForbidden,
			"message": "insufficient permissions",
		})
	}

	app := fiber.New()
	middlewares := []fiber.Handler{
		withSession,
		requireAllowed,
		handler,
	}

	switch method {
	case http.MethodGet:
		app.Get(path, middlewares[0], middlewares[1], middlewares[2])
	case http.MethodPost:
		app.Post(path, middlewares[0], middlewares[1], middlewares[2])
	case http.MethodPut:
		app.Put(path, middlewares[0], middlewares[1], middlewares[2])
	case http.MethodDelete:
		app.Delete(path, middlewares[0], middlewares[1], middlewares[2])
	default:
		panic("unsupported method")
	}

	return app
}

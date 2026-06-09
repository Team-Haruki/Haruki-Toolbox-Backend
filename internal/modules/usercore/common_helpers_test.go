package usercore

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestRequireSelfUserParam(t *testing.T) {
	t.Parallel()

	t.Run("allow when target matches authenticated user", func(t *testing.T) {
		t.Parallel()

		app := fiber.New()
		app.Get("/users/:toolbox_user_id/resource",
			func(c fiber.Ctx) error {
				c.Locals("userID", "u-123")
				return c.Next()
			},
			RequireSelfUserParam("toolbox_user_id"),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) },
		)

		resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/users/u-123/resource", nil))
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
	})

	t.Run("reject when target does not match authenticated user", func(t *testing.T) {
		t.Parallel()

		app := fiber.New()
		app.Get("/users/:toolbox_user_id/resource",
			func(c fiber.Ctx) error {
				c.Locals("userID", "u-123")
				return c.Next()
			},
			RequireSelfUserParam("toolbox_user_id"),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) },
		)

		resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/users/u-456/resource", nil))
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusForbidden {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
		}
	})

	t.Run("reject when user is not authenticated", func(t *testing.T) {
		t.Parallel()

		app := fiber.New()
		app.Get("/users/:toolbox_user_id/resource",
			RequireSelfUserParam("toolbox_user_id"),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) },
		)

		resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/users/u-123/resource", nil))
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
		}
	})

	t.Run("reject when configured param does not exist", func(t *testing.T) {
		t.Parallel()

		app := fiber.New()
		app.Get("/users/:other/resource",
			func(c fiber.Ctx) error {
				c.Locals("userID", "u-123")
				return c.Next()
			},
			RequireSelfUserParam("toolbox_user_id"),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) },
		)

		resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/users/u-123/resource", nil))
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}

func TestCurrentKratosIdentityID(t *testing.T) {
	t.Parallel()

	t.Run("returns identity id from locals", func(t *testing.T) {
		t.Parallel()

		app := fiber.New()
		app.Get("/identity", func(c fiber.Ctx) error {
			c.Locals("identityID", "kratos-1")
			identityID, err := CurrentKratosIdentityID(c)
			if err != nil {
				t.Fatalf("CurrentKratosIdentityID returned error: %v", err)
			}
			return c.SendString(identityID)
		})

		resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/identity", nil))
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
	})

	t.Run("fails when missing identity id", func(t *testing.T) {
		t.Parallel()

		app := fiber.New()
		app.Get("/identity", func(c fiber.Ctx) error {
			_, err := CurrentKratosIdentityID(c)
			if err == nil {
				t.Fatalf("expected error when identityID missing")
			}
			return c.SendStatus(fiber.StatusUnauthorized)
		})

		resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/identity", nil))
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
		}
	})
}

func TestRequireVerifiedEmail(t *testing.T) {
	t.Parallel()

	t.Run("allow when email verified is true", func(t *testing.T) {
		t.Parallel()

		app := fiber.New()
		app.Get("/verified",
			func(c fiber.Ctx) error {
				c.Locals("emailVerified", true)
				return c.Next()
			},
			RequireVerifiedEmail(),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) },
		)

		resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/verified", nil))
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
	})

	t.Run("reject when email verified is false", func(t *testing.T) {
		t.Parallel()

		app := fiber.New()
		app.Get("/verified",
			func(c fiber.Ctx) error {
				c.Locals("emailVerified", false)
				return c.Next()
			},
			RequireVerifiedEmail(),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) },
		)

		resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/verified", nil))
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusForbidden {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
		}
	})

	t.Run("reject when email verified is missing", func(t *testing.T) {
		t.Parallel()

		app := fiber.New()
		app.Get("/verified",
			RequireVerifiedEmail(),
			func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) },
		)

		resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/verified", nil))
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusForbidden {
			t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
		}
	})
}

func TestComposedUserGuards(t *testing.T) {
	t.Parallel()

	apiHelper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		SessionHandler: harukiAPIHelper.NewSessionHandler(nil, ""),
	}

	cases := []struct {
		name     string
		handlers []fiber.Handler
		wantLen  int
	}{
		{
			name:     "authenticated user",
			handlers: RequireAuthenticatedUser(apiHelper),
			wantLen:  2,
		},
		{
			name:     "authenticated self",
			handlers: RequireAuthenticatedSelf(apiHelper, "toolbox_user_id"),
			wantLen:  3,
		},
		{
			name:     "authenticated verified self",
			handlers: RequireAuthenticatedVerifiedSelf(apiHelper, "toolbox_user_id"),
			wantLen:  4,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.handlers) != tt.wantLen {
				t.Fatalf("len = %d, want %d", len(tt.handlers), tt.wantLen)
			}
			for i, handler := range tt.handlers {
				if handler == nil {
					t.Fatalf("handler %d is nil", i)
				}
			}
		})
	}
}

func TestRouteHandlersAppendsRouteHandlers(t *testing.T) {
	t.Parallel()

	first := func(c fiber.Ctx) error { return c.Next() }
	second := func(c fiber.Ctx) error { return c.Next() }
	third := func(c fiber.Ctx) error { return c.Next() }

	handlers := RouteHandlers([]fiber.Handler{first, second}, third)
	if len(handlers) != 3 {
		t.Fatalf("len = %d, want 3", len(handlers))
	}
	for i, handler := range handlers {
		if handler == nil {
			t.Fatalf("handler %d is nil", i)
		}
		if _, ok := handler.(fiber.Handler); !ok {
			t.Fatalf("handler %d type = %T, want fiber.Handler", i, handler)
		}
	}
}

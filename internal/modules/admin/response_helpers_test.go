package admin

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestRespondFiberOrHelpers(t *testing.T) {
	t.Parallel()

	run := func(t *testing.T, handler fiber.Handler) int {
		t.Helper()
		app := fiber.New()
		app.Get("/", handler)
		resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/", nil))
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		return resp.StatusCode
	}

	t.Run("forbidden with fiber error", func(t *testing.T) {
		t.Parallel()
		status := run(t, func(c fiber.Ctx) error {
			return respondFiberOrForbidden(c, fiber.NewError(fiber.StatusForbidden, "denied"), "fallback")
		})
		if status != fiber.StatusForbidden {
			t.Fatalf("status = %d, want %d", status, fiber.StatusForbidden)
		}
	})

	t.Run("forbidden with plain error", func(t *testing.T) {
		t.Parallel()
		status := run(t, func(c fiber.Ctx) error {
			return respondFiberOrForbidden(c, errors.New("plain"), "fallback")
		})
		if status != fiber.StatusForbidden {
			t.Fatalf("status = %d, want %d", status, fiber.StatusForbidden)
		}
	})

	t.Run("bad request with plain error", func(t *testing.T) {
		t.Parallel()
		status := run(t, func(c fiber.Ctx) error {
			return respondFiberOrBadRequest(c, errors.New("plain"), "fallback")
		})
		if status != fiber.StatusBadRequest {
			t.Fatalf("status = %d, want %d", status, fiber.StatusBadRequest)
		}
	})

	t.Run("internal with plain error", func(t *testing.T) {
		t.Parallel()
		status := run(t, func(c fiber.Ctx) error {
			return respondFiberOrInternal(c, errors.New("plain"), "fallback")
		})
		if status != fiber.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", status, fiber.StatusInternalServerError)
		}
	})

	t.Run("unauthorized with plain error", func(t *testing.T) {
		t.Parallel()
		status := run(t, func(c fiber.Ctx) error {
			return respondFiberOrUnauthorized(c, errors.New("plain"), "fallback")
		})
		if status != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", status, fiber.StatusUnauthorized)
		}
	})
}

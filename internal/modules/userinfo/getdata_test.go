package userinfo

import (
	"errors"
	"haruki-suite/utils/database/postgresql"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestMapOwnedBindingLookupError(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if got := mapOwnedBindingLookupError(nil); got != nil {
			t.Fatalf("mapOwnedBindingLookupError(nil) = %#v, want nil", got)
		}
	})

	t.Run("not found", func(t *testing.T) {
		got := mapOwnedBindingLookupError(&postgresql.NotFoundError{})
		if got == nil {
			t.Fatalf("expected non-nil error")
		}
		if got.Code != fiber.StatusNotFound {
			t.Fatalf("status = %d, want %d", got.Code, fiber.StatusNotFound)
		}
		if got.Message != "game account binding not found or not owned by you" {
			t.Fatalf("message = %q", got.Message)
		}
	})

	t.Run("internal", func(t *testing.T) {
		got := mapOwnedBindingLookupError(errors.New("db unavailable"))
		if got == nil {
			t.Fatalf("expected non-nil error")
		}
		if got.Code != fiber.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", got.Code, fiber.StatusInternalServerError)
		}
		if got.Message != "failed to query game account binding" {
			t.Fatalf("message = %q", got.Message)
		}
	})
}

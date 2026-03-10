package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestTrimAndLimit(t *testing.T) {
	t.Parallel()

	if got := trimAndLimit("  abc  ", 0); got != "abc" {
		t.Fatalf("trimAndLimit trim failed, got %q", got)
	}
	if got := trimAndLimit("abcdef", 3); got != "abc" {
		t.Fatalf("trimAndLimit limit failed, got %q", got)
	}
}

func TestNormalizeSystemLogActorType(t *testing.T) {
	t.Parallel()

	if got := string(normalizeSystemLogActorType("user")); got != SystemLogActorTypeUser {
		t.Fatalf("actor type user normalize failed: %q", got)
	}
	if got := string(normalizeSystemLogActorType("ADMIN")); got != SystemLogActorTypeAdmin {
		t.Fatalf("actor type admin normalize failed: %q", got)
	}
	if got := string(normalizeSystemLogActorType("system")); got != SystemLogActorTypeSystem {
		t.Fatalf("actor type system normalize failed: %q", got)
	}
	if got := string(normalizeSystemLogActorType("unknown")); got != SystemLogActorTypeAnonymous {
		t.Fatalf("actor type default normalize failed: %q", got)
	}
}

func TestNormalizeSystemLogResult(t *testing.T) {
	t.Parallel()

	if got := string(normalizeSystemLogResult("failure")); got != SystemLogResultFailure {
		t.Fatalf("result failure normalize failed: %q", got)
	}
	if got := string(normalizeSystemLogResult("SUCCESS")); got != SystemLogResultSuccess {
		t.Fatalf("result success normalize failed: %q", got)
	}
	if got := string(normalizeSystemLogResult("whatever")); got != SystemLogResultSuccess {
		t.Fatalf("result default normalize failed: %q", got)
	}
}

func TestBuildSystemLogEntryFromFiber(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		c.Locals("userID", "u1")
		c.Locals("userRole", "ADMIN")

		targetType := "user"
		targetID := "u2"
		entry := BuildSystemLogEntryFromFiber(c, " user.login ", "success", &targetType, &targetID, map[string]any{"a": 1})

		if entry.ActorUserID == nil || *entry.ActorUserID != "u1" {
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		if entry.ActorRole == nil || *entry.ActorRole != "admin" {
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		if entry.ActorType != SystemLogActorTypeAdmin {
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		if entry.Action != "user.login" {
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
	}
}

func TestWriteSystemLogNoopWhenHelperMissing(t *testing.T) {
	t.Parallel()

	err := WriteSystemLog(context.Background(), nil, SystemLogEntry{Action: "user.login"})
	if err != nil {
		t.Fatalf("WriteSystemLog should ignore nil helper, got %v", err)
	}
}

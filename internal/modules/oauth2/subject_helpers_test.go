package oauth2

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestHydraSubjectsForUser(t *testing.T) {
	t.Parallel()

	identityID := "kratos-1"
	subjects := HydraSubjectsForUser("u-1", &identityID)
	if len(subjects) != 2 {
		t.Fatalf("len(subjects) = %d, want 2", len(subjects))
	}
	if subjects[0] != "kratos-1" || subjects[1] != "u-1" {
		t.Fatalf("subjects = %#v, want [kratos-1 u-1]", subjects)
	}

	sameIdentityID := "u-1"
	subjects = HydraSubjectsForUser("u-1", &sameIdentityID)
	if len(subjects) != 1 || subjects[0] != "u-1" {
		t.Fatalf("subjects with duplicate identity = %#v, want [u-1]", subjects)
	}
}

func TestCurrentHydraSubjectPrefersIdentityID(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Get("/subject", func(c fiber.Ctx) error {
		c.Locals("userID", "u-1")
		c.Locals("identityID", "kratos-1")

		subject, err := CurrentHydraSubject(c)
		if err != nil {
			t.Fatalf("CurrentHydraSubject returned error: %v", err)
		}
		if subject != "kratos-1" {
			t.Fatalf("subject = %q, want %q", subject, "kratos-1")
		}
		if err := CurrentHydraSubjectMatches(c, "u-1"); err != nil {
			t.Fatalf("CurrentHydraSubjectMatches should accept legacy user id: %v", err)
		}
		if err := CurrentHydraSubjectMatches(c, "kratos-1"); err != nil {
			t.Fatalf("CurrentHydraSubjectMatches should accept kratos identity id: %v", err)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/subject", nil))
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
	}
}

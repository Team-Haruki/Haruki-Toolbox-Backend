package admintickets

import (
	"haruki-suite/utils/database/postgresql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func TestParseAdminTicketFilters(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		_, err := parseAdminTicketFilters(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.SendStatus(fiberErr.Code)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	t.Run("valid filters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?q=abc&status=open&priority=high&creator_user_id=1001&assignee_admin_id=2001&page=2&page_size=20", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusNoContent {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNoContent)
		}
	})

	t.Run("invalid status", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?status=done", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}

func TestBuildAdminTicketListItemIncludesCreatorInfo(t *testing.T) {
	now := time.Now().UTC()
	row := &postgresql.Ticket{
		TicketID:      "TK-20260308180000-abcdef123456",
		CreatorUserID: "1241241241",
		Subject:       "upload failed",
		Priority:      "high",
		Status:        "open",
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	item := buildAdminTicketListItem(row, map[string]string{
		"1241241241": "test-user",
	})
	if item.CreatorUserID != "1241241241" {
		t.Fatalf("CreatorUserID = %q, want %q", item.CreatorUserID, "1241241241")
	}
	if item.CreatorUserName != "test-user" {
		t.Fatalf("CreatorUserName = %q, want %q", item.CreatorUserName, "test-user")
	}

	item = buildAdminTicketListItem(row, nil)
	if item.CreatorUserName != "" {
		t.Fatalf("CreatorUserName = %q, want empty string", item.CreatorUserName)
	}
}

package admin

import (
	"bytes"
	"encoding/json"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/gofiber/fiber/v3"
)

func newAdminTicketNotificationTestHelper(t *testing.T) *harukiAPIHelper.HarukiToolboxRouterHelpers {
	t.Helper()

	client := enttest.Open(t, "sqlite3", "file:admin-ticket-notification-test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() {
		_ = client.Close()
	})

	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{
			DB: client,
		},
	}
	return helper
}

func seedAdminTicketNotificationTestUser(t *testing.T, helper *harukiAPIHelper.HarukiToolboxRouterHelpers, enabled bool) {
	t.Helper()

	if _, err := helper.DBManager.DB.User.Create().
		SetID("admin-1").
		SetName("Admin One").
		SetEmail("admin1@example.com").
		SetRole("admin").
		SetTicketEmailNotificationsEnabled(enabled).
		Save(t.Context()); err != nil {
		t.Fatalf("failed to seed admin user: %v", err)
	}
}

func TestHandleGetAdminTicketNotificationPreference(t *testing.T) {
	helper := newAdminTicketNotificationTestHelper(t)
	seedAdminTicketNotificationTestUser(t, helper, true)

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "admin-1")
		c.Locals("userRole", roleAdmin)
		return c.Next()
	})
	app.Get("/", handleGetAdminTicketNotificationPreference(helper))

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}
}

func TestHandleUpdateAdminTicketNotificationPreference(t *testing.T) {
	helper := newAdminTicketNotificationTestHelper(t)
	seedAdminTicketNotificationTestUser(t, helper, false)

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "admin-1")
		c.Locals("userRole", roleSuperAdmin)
		return c.Next()
	})
	app.Put("/", handleUpdateAdminTicketNotificationPreference(helper))

	body, err := json.Marshal(map[string]any{
		"ticketEmailNotificationsEnabled": true,
	})
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}

	dbUser, err := helper.DBManager.DB.User.Get(t.Context(), "admin-1")
	if err != nil {
		t.Fatalf("failed to query updated user: %v", err)
	}
	if !dbUser.TicketEmailNotificationsEnabled {
		t.Fatalf("TicketEmailNotificationsEnabled = false, want true")
	}
}

func TestHandleUpdateAdminTicketNotificationPreferenceRejectsMissingField(t *testing.T) {
	helper := newAdminTicketNotificationTestHelper(t)
	seedAdminTicketNotificationTestUser(t, helper, false)

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "admin-1")
		c.Locals("userRole", roleAdmin)
		return c.Next()
	})
	app.Put("/", handleUpdateAdminTicketNotificationPreference(helper))

	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
	}
}

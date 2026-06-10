package adminusers

import (
	"bytes"
	"encoding/json"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/gofiber/fiber/v3"
)

func newAdminUserTicketNotificationTestHelper(t *testing.T) *harukiAPIHelper.HarukiToolboxRouterHelpers {
	t.Helper()

	client := enttest.Open(t, "sqlite3", "file:admin-user-ticket-notification-test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() {
		_ = client.Close()
	})

	return &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{
			DB: client,
		},
	}
}

func seedAdminUserTicketNotificationTestUser(t *testing.T, helper *harukiAPIHelper.HarukiToolboxRouterHelpers, id, name, email, role string, enabled, banned bool) {
	t.Helper()

	if _, err := helper.DBManager.DB.User.Create().
		SetID(id).
		SetName(name).
		SetEmail(email).
		SetRole(userSchema.Role(role)).
		SetTicketEmailNotificationsEnabled(enabled).
		SetBanned(banned).
		Save(t.Context()); err != nil {
		t.Fatalf("failed to seed user %s: %v", id, err)
	}
}

func TestParseAdminTicketNotificationPreferencePayload(t *testing.T) {
	app := fiber.New()
	app.Put("/", func(c fiber.Ctx) error {
		payload, err := parseAdminTicketNotificationPreferencePayload(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.SendStatus(fiberErr.Code)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}

		if payload.TicketEmailNotificationsEnabled == nil {
			return c.SendStatus(fiber.StatusBadRequest)
		}
		if *payload.TicketEmailNotificationsEnabled {
			return c.SendString("true")
		}
		return c.SendString("false")
	})

	t.Run("camel case payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader([]byte(`{"ticketEmailNotificationsEnabled":true}`)))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
	})

	t.Run("snake case payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader([]byte(`{"ticket_email_notifications_enabled":false}`)))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
	})

	t.Run("missing field", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader([]byte(`{}`)))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}

func TestHandleListAdminTicketNotificationRecipients(t *testing.T) {
	helper := newAdminUserTicketNotificationTestHelper(t)
	seedAdminUserTicketNotificationTestUser(t, helper, "admin-1", "Admin One", "admin1@example.com", roleAdmin, true, false)
	seedAdminUserTicketNotificationTestUser(t, helper, "super-1", "Super One", "super1@example.com", roleSuperAdmin, false, false)
	seedAdminUserTicketNotificationTestUser(t, helper, "user-1", "User One", "user@example.com", roleUser, false, false)

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "super-1")
		c.Locals("userRole", roleSuperAdmin)
		return c.Next()
	})
	app.Get("/", handleListAdminTicketNotificationRecipients(helper))

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}

	var decoded struct {
		Status      int                                       `json:"status"`
		Message     string                                    `json:"message"`
		UpdatedData adminTicketNotificationRecipientsResponse `json:"updatedData"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("json decode returned error: %v", err)
	}
	if decoded.UpdatedData.Total != 2 {
		t.Fatalf("total = %d, want 2", decoded.UpdatedData.Total)
	}
	if len(decoded.UpdatedData.Items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(decoded.UpdatedData.Items))
	}
	if decoded.UpdatedData.Items[0].Role != roleSuperAdmin {
		t.Fatalf("items[0].Role = %q, want %q", decoded.UpdatedData.Items[0].Role, roleSuperAdmin)
	}
}

func TestHandleUpdateUserTicketNotificationPreference(t *testing.T) {
	helper := newAdminUserTicketNotificationTestHelper(t)
	seedAdminUserTicketNotificationTestUser(t, helper, "admin-1", "Admin One", "admin1@example.com", roleAdmin, false, false)
	seedAdminUserTicketNotificationTestUser(t, helper, "super-1", "Super One", "super1@example.com", roleSuperAdmin, false, false)

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "super-1")
		c.Locals("userRole", roleSuperAdmin)
		return c.Next()
	})
	app.Put("/:target_user_id/ticket-notifications", handleUpdateUserTicketNotificationPreference(helper))

	body, err := json.Marshal(map[string]any{
		"ticketEmailNotificationsEnabled": true,
	})
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/admin-1/ticket-notifications", bytes.NewReader(body))
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

func TestHandleUpdateUserTicketNotificationPreferenceRejectsNormalUserTarget(t *testing.T) {
	helper := newAdminUserTicketNotificationTestHelper(t)
	seedAdminUserTicketNotificationTestUser(t, helper, "user-1", "User One", "user@example.com", roleUser, false, false)
	seedAdminUserTicketNotificationTestUser(t, helper, "super-1", "Super One", "super1@example.com", roleSuperAdmin, false, false)

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "super-1")
		c.Locals("userRole", roleSuperAdmin)
		return c.Next()
	})
	app.Put("/:target_user_id/ticket-notifications", handleUpdateUserTicketNotificationPreference(helper))

	body, err := json.Marshal(map[string]any{
		"ticketEmailNotificationsEnabled": true,
	})
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/user-1/ticket-notifications", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
	}
}

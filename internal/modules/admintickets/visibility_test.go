package admintickets

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	ticketsModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/tickets"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticket"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

func newAdminTicketVisibilityTestHelper(t *testing.T) *harukiAPIHelper.HarukiToolboxRouterHelpers {
	t.Helper()

	dbName := strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())
	client := enttest.Open(t, "sqlite3", "file:"+dbName+"?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() {
		_ = client.Close()
	})
	return &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{DB: client},
	}
}

func seedAdminTicketVisibilityUser(t *testing.T, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, id, role string) {
	t.Helper()
	if _, err := apiHelper.DBManager.DB.User.Create().
		SetID(id).
		SetName(id).
		SetEmail(id + "@example.com").
		SetRole(userSchema.Role(role)).
		Save(t.Context()); err != nil {
		t.Fatalf("seed user %q: %v", id, err)
	}
}

func seedAdminTicketVisibilityTicket(
	t *testing.T,
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	ticketID string,
	creatorUserID string,
	updatedAt time.Time,
) {
	t.Helper()
	if _, err := apiHelper.DBManager.DB.Ticket.Create().
		SetTicketID(ticketID).
		SetCreatorUserID(creatorUserID).
		SetSubject(ticketID).
		SetCreatedAt(updatedAt).
		SetUpdatedAt(updatedAt).
		Save(t.Context()); err != nil {
		t.Fatalf("seed ticket %q: %v", ticketID, err)
	}
}

func newAdminTicketVisibilityTestApp(actorUserID, actorRole string) *fiber.App {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", actorUserID)
		c.Locals("userRole", actorRole)
		return c.Next()
	})
	return app
}

func TestHandleAdminListTicketsScopesCountAndPaginationByCreatorRole(t *testing.T) {
	apiHelper := newAdminTicketVisibilityTestHelper(t)
	seedAdminTicketVisibilityUser(t, apiHelper, "admin-actor", adminCoreModule.RoleAdmin)
	seedAdminTicketVisibilityUser(t, apiHelper, "super-actor", adminCoreModule.RoleSuperAdmin)
	seedAdminTicketVisibilityUser(t, apiHelper, "user-creator", adminCoreModule.RoleUser)
	seedAdminTicketVisibilityUser(t, apiHelper, "admin-creator", adminCoreModule.RoleAdmin)
	seedAdminTicketVisibilityUser(t, apiHelper, "super-creator", adminCoreModule.RoleSuperAdmin)

	now := time.Now().UTC()
	seedAdminTicketVisibilityTicket(t, apiHelper, "ticket-visible-new", "user-creator", now.Add(3*time.Minute))
	seedAdminTicketVisibilityTicket(t, apiHelper, "ticket-hidden-middle", "super-creator", now.Add(2*time.Minute))
	seedAdminTicketVisibilityTicket(t, apiHelper, "ticket-visible-old", "admin-creator", now.Add(time.Minute))

	t.Run("plain admin excludes super admin tickets before count and pagination", func(t *testing.T) {
		app := newAdminTicketVisibilityTestApp("admin-actor", adminCoreModule.RoleAdmin)
		app.Get("/tickets", handleAdminListTickets(apiHelper))

		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/tickets?page=2&page_size=1", nil))
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}

		var decoded struct {
			UpdatedData adminTicketListResponse `json:"updatedData"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if decoded.UpdatedData.Total != 2 {
			t.Fatalf("total = %d, want 2", decoded.UpdatedData.Total)
		}
		if decoded.UpdatedData.TotalPages != 2 {
			t.Fatalf("totalPages = %d, want 2", decoded.UpdatedData.TotalPages)
		}
		if len(decoded.UpdatedData.Items) != 1 {
			t.Fatalf("items length = %d, want 1", len(decoded.UpdatedData.Items))
		}
		if got := decoded.UpdatedData.Items[0].TicketID; got != "ticket-visible-old" {
			t.Fatalf("ticket ID = %q, want ticket-visible-old", got)
		}
	})

	t.Run("super admin retains the complete list", func(t *testing.T) {
		app := newAdminTicketVisibilityTestApp("super-actor", adminCoreModule.RoleSuperAdmin)
		app.Get("/tickets", handleAdminListTickets(apiHelper))

		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/tickets?page=1&page_size=10", nil))
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		defer resp.Body.Close()

		var decoded struct {
			UpdatedData adminTicketListResponse `json:"updatedData"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if decoded.UpdatedData.Total != 3 || len(decoded.UpdatedData.Items) != 3 {
			t.Fatalf("total/items = %d/%d, want 3/3", decoded.UpdatedData.Total, len(decoded.UpdatedData.Items))
		}
	})
}

func TestAdminTicketOperationsRejectSuperAdminCreatorForPlainAdmin(t *testing.T) {
	apiHelper := newAdminTicketVisibilityTestHelper(t)
	seedAdminTicketVisibilityUser(t, apiHelper, "admin-actor", adminCoreModule.RoleAdmin)
	seedAdminTicketVisibilityUser(t, apiHelper, "admin-target", adminCoreModule.RoleAdmin)
	seedAdminTicketVisibilityUser(t, apiHelper, "super-creator", adminCoreModule.RoleSuperAdmin)
	seedAdminTicketVisibilityUser(t, apiHelper, "super-actor", adminCoreModule.RoleSuperAdmin)
	seedAdminTicketVisibilityTicket(t, apiHelper, "ticket-super", "super-creator", time.Now().UTC())

	app := newAdminTicketVisibilityTestApp("admin-actor", adminCoreModule.RoleAdmin)
	app.Get("/tickets/:ticket_id", handleAdminGetTicketDetail(apiHelper))
	app.Post("/tickets/:ticket_id/messages", handleAdminAppendTicketMessage(apiHelper, ticketsModule.NotificationConfig{}))
	app.Put("/tickets/:ticket_id/status", handleAdminUpdateTicketStatus(apiHelper))
	app.Put("/tickets/:ticket_id/assign", handleAdminAssignTicket(apiHelper))

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "detail", method: http.MethodGet, path: "/tickets/ticket-super"},
		{name: "reply", method: http.MethodPost, path: "/tickets/ticket-super/messages", body: `{"message":"not allowed","internal":true}`},
		{name: "status", method: http.MethodPut, path: "/tickets/ticket-super/status", body: `{"status":"resolved"}`},
		{name: "assign", method: http.MethodPut, path: "/tickets/ticket-super/assign", body: `{"assigneeAdminId":"admin-target"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test returned error: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != fiber.StatusForbidden {
				t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
			}
		})
	}

	row, err := apiHelper.DBManager.DB.Ticket.Query().
		Where(ticket.TicketIDEQ("ticket-super")).
		Only(t.Context())
	if err != nil {
		t.Fatalf("query ticket: %v", err)
	}
	if row.Status != ticket.StatusOpen {
		t.Fatalf("status = %q, want %q", row.Status, ticket.StatusOpen)
	}
	if row.AssigneeAdminID != nil {
		t.Fatalf("assignee = %q, want nil", *row.AssigneeAdminID)
	}
	messageCount, err := row.QueryMessages().Count(t.Context())
	if err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if messageCount != 0 {
		t.Fatalf("message count = %d, want 0", messageCount)
	}

	superApp := newAdminTicketVisibilityTestApp("super-actor", adminCoreModule.RoleSuperAdmin)
	superApp.Get("/tickets/:ticket_id", handleAdminGetTicketDetail(apiHelper))
	resp, err := superApp.Test(httptest.NewRequest(http.MethodGet, "/tickets/ticket-super", nil))
	if err != nil {
		t.Fatalf("super admin detail request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("super admin detail status = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}
}

func TestHandleAdminAssignTicketEnforcesAssigneeRoleBoundary(t *testing.T) {
	apiHelper := newAdminTicketVisibilityTestHelper(t)
	seedAdminTicketVisibilityUser(t, apiHelper, "admin-actor", adminCoreModule.RoleAdmin)
	seedAdminTicketVisibilityUser(t, apiHelper, "user-creator", adminCoreModule.RoleUser)
	seedAdminTicketVisibilityUser(t, apiHelper, "super-target", adminCoreModule.RoleSuperAdmin)
	seedAdminTicketVisibilityTicket(t, apiHelper, "ticket-user", "user-creator", time.Now().UTC())

	app := newAdminTicketVisibilityTestApp("admin-actor", adminCoreModule.RoleAdmin)
	app.Put("/tickets/:ticket_id/assign", handleAdminAssignTicket(apiHelper))

	req := httptest.NewRequest(http.MethodPut, "/tickets/ticket-user/assign", strings.NewReader(`{"assigneeAdminId":"super-target"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
	}

	row, err := apiHelper.DBManager.DB.Ticket.Query().
		Where(ticket.TicketIDEQ("ticket-user")).
		Only(t.Context())
	if err != nil {
		t.Fatalf("query ticket: %v", err)
	}
	if row.AssigneeAdminID != nil {
		t.Fatalf("assignee = %q, want nil", *row.AssigneeAdminID)
	}
}

func TestHandleAdminAssignTicketKeepsSelfAssignmentAvailable(t *testing.T) {
	apiHelper := newAdminTicketVisibilityTestHelper(t)
	seedAdminTicketVisibilityUser(t, apiHelper, "admin-actor", adminCoreModule.RoleAdmin)
	seedAdminTicketVisibilityUser(t, apiHelper, "user-creator", adminCoreModule.RoleUser)
	seedAdminTicketVisibilityTicket(t, apiHelper, "ticket-user", "user-creator", time.Now().UTC())

	app := newAdminTicketVisibilityTestApp("admin-actor", adminCoreModule.RoleAdmin)
	app.Put("/tickets/:ticket_id/assign", handleAdminAssignTicket(apiHelper))

	req := httptest.NewRequest(http.MethodPut, "/tickets/ticket-user/assign", strings.NewReader(`{"assigneeAdminId":"admin-actor"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}

	row, err := apiHelper.DBManager.DB.Ticket.Query().
		Where(ticket.TicketIDEQ("ticket-user")).
		Only(t.Context())
	if err != nil {
		t.Fatalf("query ticket: %v", err)
	}
	if row.AssigneeAdminID == nil || *row.AssigneeAdminID != "admin-actor" {
		t.Fatalf("assignee = %#v, want admin-actor", row.AssigneeAdminID)
	}
}

package admintickets

import (
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	adminCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/admincore"
	ticketsModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/tickets"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticket"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticketmessage"
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
		if err := json.UnmarshalRead(resp.Body, &decoded); err != nil {
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
		if err := json.UnmarshalRead(resp.Body, &decoded); err != nil {
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
			if resp.StatusCode != fiber.StatusNotFound {
				t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusNotFound)
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

func TestAdminTicketScopeCoversAssigneeAndMessageAuthors(t *testing.T) {
	helper := newAdminTicketVisibilityTestHelper(t)
	seedAdminTicketVisibilityUser(t, helper, "admin", adminCoreModule.RoleAdmin)
	seedAdminTicketVisibilityUser(t, helper, "super", adminCoreModule.RoleSuperAdmin)
	seedAdminTicketVisibilityUser(t, helper, "creator", adminCoreModule.RoleUser)
	db := helper.DBManager.DB
	db.Ticket.Create().SetTicketID("assigned").SetCreatorUserID("creator").SetSubject("fixture").SetAssigneeAdminID("super").ExecX(t.Context())
	note := db.Ticket.Create().SetTicketID("noted").SetCreatorUserID("creator").SetSubject("fixture").SaveX(t.Context())
	db.TicketMessage.Create().SetTicketID(note.ID).SetSenderUserID("super").SetSenderRole(ticketmessage.SenderRoleAdmin).
		SetInternal(true).SetMessage("restricted").ExecX(t.Context())
	app := newAdminTicketVisibilityTestApp("admin", adminCoreModule.RoleAdmin)
	app.Get("/tickets", handleAdminListTickets(helper))
	app.Get("/tickets/:ticket_id", handleAdminGetTicketDetail(helper))
	app.Put("/tickets/:ticket_id/assign", handleAdminAssignTicket(helper))
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/tickets", nil))
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		UpdatedData adminTicketListResponse `json:"updatedData"`
	}
	err = json.UnmarshalRead(resp.Body, &list)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK || list.UpdatedData.Total != 1 || len(list.UpdatedData.Items) != 1 || list.UpdatedData.Items[0].TicketID != "noted" || list.UpdatedData.Items[0].LastMessagePreview != "" {
		t.Fatalf("protected tickets listed: status=%d total=%d err=%v", resp.StatusCode, list.UpdatedData.Total, err)
	}
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/tickets/assigned", ""},
		{http.MethodPut, "/tickets/assigned/assign", `{"assigneeAdminId":null}`},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s %s: %d", tc.method, tc.path, resp.StatusCode)
		}
	}
	resp, err = app.Test(httptest.NewRequest(http.MethodGet, "/tickets/noted", nil))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK || strings.Contains(string(data), "restricted") || strings.Contains(string(data), `"senderUserId":"super"`) {
		t.Fatalf("visible ticket revealed protected author or note: status=%d err=%v", resp.StatusCode, err)
	}
	assigned := db.Ticket.Query().Where(ticket.TicketIDEQ("assigned")).OnlyX(t.Context())
	if assigned.AssigneeAdminID == nil || *assigned.AssigneeAdminID != "super" {
		t.Fatal("protected assignment was changed")
	}
}

func TestAdminTicketDetailHidesLegacyAssignmentHistoryFromOrdinaryAdmins(t *testing.T) {
	helper := newAdminTicketVisibilityTestHelper(t)
	seedAdminTicketVisibilityUser(t, helper, "admin", adminCoreModule.RoleAdmin)
	seedAdminTicketVisibilityUser(t, helper, "super", adminCoreModule.RoleSuperAdmin)
	seedAdminTicketVisibilityUser(t, helper, "creator", adminCoreModule.RoleUser)
	db := helper.DBManager.DB
	row := db.Ticket.Create().SetTicketID("history").SetCreatorUserID("creator").SetSubject("test").SaveX(t.Context())
	visible := db.TicketMessage.Create().SetTicketID(row.ID).SetSenderUserID("creator").SetMessage("ticket contents").SaveX(t.Context())
	db.TicketMessage.Create().SetTicketID(row.ID).SetSenderRole(ticketmessage.SenderRoleSystem).SetInternal(true).
		SetMessage(ticketsModule.AssignmentEventPrefix + "super -> Unassigned").ExecX(t.Context())
	for _, tc := range []struct {
		actor, role string
		messages    int
	}{
		{"admin", adminCoreModule.RoleAdmin, 1}, {"super", adminCoreModule.RoleSuperAdmin, 2},
	} {
		app := newAdminTicketVisibilityTestApp(tc.actor, tc.role)
		app.Get("/tickets/:ticket_id", handleAdminGetTicketDetail(helper))
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/tickets/history", nil))
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			UpdatedData adminTicketDetailResponse `json:"updatedData"`
		}
		err = json.UnmarshalRead(resp.Body, &body)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK || len(body.UpdatedData.Messages) != tc.messages || body.UpdatedData.Messages[0].ID != visible.ID {
			t.Fatalf("unexpected %s ticket detail: status %d, messages %d, err %v", tc.role, resp.StatusCode, len(body.UpdatedData.Messages), err)
		}
	}
}

func TestAdminTicketViewsHideAssignmentParticipantsAfterPromotion(t *testing.T) {
	helper := newAdminTicketVisibilityTestHelper(t)
	const promotedID = "future-admin"
	const promotedName = "Protected account display name"
	seedAdminTicketVisibilityUser(t, helper, promotedID, adminCoreModule.RoleAdmin)
	seedAdminTicketVisibilityUser(t, helper, "support", adminCoreModule.RoleAdmin)
	seedAdminTicketVisibilityUser(t, helper, "supervisor", adminCoreModule.RoleSuperAdmin)
	seedAdminTicketVisibilityUser(t, helper, "creator", adminCoreModule.RoleUser)
	db := helper.DBManager.DB
	db.User.UpdateOneID(promotedID).SetName(promotedName).ExecX(t.Context())
	row := db.Ticket.Create().SetTicketID("history").SetCreatorUserID("creator").SetSubject("test").SaveX(t.Context())
	db.TicketMessage.Create().SetTicketID(row.ID).SetSenderUserID("creator").SetMessage("ticket contents").ExecX(t.Context())

	testApp := func(actor, role string) *fiber.App {
		app := newAdminTicketVisibilityTestApp(actor, role)
		app.Get("/tickets", handleAdminListTickets(helper))
		app.Get("/tickets/:ticket_id", handleAdminGetTicketDetail(helper))
		app.Put("/tickets/:ticket_id/assign", handleAdminAssignTicket(helper))
		return app
	}
	futureAdmin := testApp(promotedID, adminCoreModule.RoleAdmin)
	ordinary := testApp("support", adminCoreModule.RoleAdmin)
	supervisor := testApp("supervisor", adminCoreModule.RoleSuperAdmin)
	const assignURL = "/tickets/history/assign"
	const assignToFutureAdmin = `{"assigneeAdminId":"future-admin"}`
	const unassign = `{"assigneeAdminId":null}`
	requestAdminTicketView(t, futureAdmin, http.MethodPut, assignURL, assignToFutureAdmin, http.StatusOK)
	// This event references the promoted user but is attributed to another
	// ordinary administrator. Filtering by the sender alone misses it.
	requestAdminTicketView(t, ordinary, http.MethodPut, assignURL, unassign, http.StatusOK)
	requestAdminTicketView(t, futureAdmin, http.MethodPut, assignURL, assignToFutureAdmin, http.StatusOK)
	db.User.UpdateOneID(promotedID).SetRole(userSchema.RoleSuperAdmin).ExecX(t.Context())
	requestAdminTicketView(t, ordinary, http.MethodGet, "/tickets/history", "", http.StatusNotFound)
	requestAdminTicketView(t, supervisor, http.MethodPut, assignURL, `{"assigneeAdminId":"support"}`, http.StatusOK)
	requestAdminTicketView(t, supervisor, http.MethodPut, assignURL, unassign, http.StatusOK)

	for _, path := range []string{"/tickets/history", "/tickets"} {
		body := string(requestAdminTicketView(t, ordinary, http.MethodGet, path, "", http.StatusOK))
		if strings.Contains(body, promotedID) || strings.Contains(body, promotedName) || !strings.Contains(body, "ticket contents") {
			t.Fatalf("%s exposed a historical assignee or hid the visible ticket", path)
		}
	}
	var history struct {
		UpdatedData adminTicketDetailResponse `json:"updatedData"`
	}
	raw := requestAdminTicketView(t, supervisor, http.MethodGet, "/tickets/history", "", http.StatusOK)
	if err := json.Unmarshal(raw, &history); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%s%s (%s) -> Unassigned", ticketsModule.AssignmentEventPrefix, promotedName, promotedID)
	for _, message := range history.UpdatedData.Messages {
		if message.SenderUserID == "support" && message.Message == want {
			return
		}
	}
	t.Fatal("super admin lost the historical assignment record")
}

func TestAdminTicketOlderPageKeepsLatestVisibleSummary(t *testing.T) {
	helper := newAdminTicketVisibilityTestHelper(t)
	seedAdminTicketVisibilityUser(t, helper, "support", adminCoreModule.RoleAdmin)
	seedAdminTicketVisibilityUser(t, helper, "supervisor", adminCoreModule.RoleSuperAdmin)
	seedAdminTicketVisibilityUser(t, helper, "creator", adminCoreModule.RoleUser)
	db := helper.DBManager.DB
	row := db.Ticket.Create().SetTicketID("thread").SetCreatorUserID("creator").SetSubject("test").SaveX(t.Context())
	stamp := time.Now().UTC().Truncate(time.Second)
	messages := make([]*postgresql.TicketMessageCreate, 0, 101)
	for i := range 101 {
		messages = append(messages, db.TicketMessage.Create().SetTicketID(row.ID).
			SetSenderUserID("creator").SetMessage(fmt.Sprintf("message %d", i)).
			SetCreatedAt(stamp.Add(time.Duration(i)*time.Second)))
	}
	stored := db.TicketMessage.CreateBulk(messages...).SaveX(t.Context())
	db.TicketMessage.Create().SetTicketID(row.ID).SetSenderUserID("supervisor").
		SetSenderRole(ticketmessage.SenderRoleAdmin).SetInternal(true).SetMessage("hidden note").
		SetCreatedAt(stamp.Add(101 * time.Second)).ExecX(t.Context())
	app := newAdminTicketVisibilityTestApp("support", adminCoreModule.RoleAdmin)
	app.Get("/tickets/:ticket_id", handleAdminGetTicketDetail(helper))
	read := func(query string, wantCursor bool) adminTicketDetailResponse {
		t.Helper()
		raw := requestAdminTicketView(t, app, http.MethodGet, "/tickets/thread"+query, "", http.StatusOK)
		if strings.Contains(string(raw), `"nextBeforeMessageId":`) != wantCursor {
			t.Fatalf("unexpected continuation field on page %q", query)
		}
		var body struct {
			UpdatedData adminTicketDetailResponse `json:"updatedData"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		item := body.UpdatedData.Ticket
		latest := stored[len(stored)-1]
		if item.LastMessagePreview != latest.Message || item.LastMessageAt == nil || !item.LastMessageAt.Equal(latest.CreatedAt) ||
			item.LastMessageSenderRole != string(latest.SenderRole) || item.LastMessageInternal == nil || *item.LastMessageInternal {
			t.Fatalf("page %q changed the latest visible message summary", query)
		}
		return body.UpdatedData
	}
	first := read("", true)
	if first.NextBeforeMessageID != stored[1].ID || len(first.Messages) != 100 {
		t.Fatal("unexpected first page")
	}
	older := read(fmt.Sprintf("?before_message_id=%d", first.NextBeforeMessageID), false)
	if len(older.Messages) != 1 || older.Messages[0].ID != stored[0].ID {
		t.Fatal("older page did not retain its own messages")
	}
}

func requestAdminTicketView(t *testing.T, app *fiber.App, method, path, body string, want int) []byte {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != want {
		t.Fatalf("%s %s: status %d, want %d, err %v", method, path, resp.StatusCode, want, err)
	}
	return raw
}

func TestOrdinaryAdminCanReplyAndResolveVisibleTicket(t *testing.T) {
	helper := newAdminTicketVisibilityTestHelper(t)
	seedAdminTicketVisibilityUser(t, helper, "admin", adminCoreModule.RoleAdmin)
	seedAdminTicketVisibilityUser(t, helper, "creator", adminCoreModule.RoleUser)
	seedAdminTicketVisibilityTicket(t, helper, "visible", "creator", time.Now().UTC())
	app := newAdminTicketVisibilityTestApp("admin", adminCoreModule.RoleAdmin)
	app.Post("/tickets/:ticket_id/messages", handleAdminAppendTicketMessage(helper, ticketsModule.NotificationConfig{}))
	app.Put("/tickets/:ticket_id/status", handleAdminUpdateTicketStatus(helper))
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/tickets/visible/messages", `{"message":"support reply"}`},
		{http.MethodPut, "/tickets/visible/status", `{"status":"resolved"}`},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s %s returned %d", tc.method, tc.path, resp.StatusCode)
		}
	}
	row := helper.DBManager.DB.Ticket.Query().Where(ticket.TicketIDEQ("visible")).OnlyX(t.Context())
	if row.Status != ticket.StatusResolved || row.QueryMessages().CountX(t.Context()) != 2 {
		t.Fatal("ordinary support could not update and reply to a visible ticket")
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

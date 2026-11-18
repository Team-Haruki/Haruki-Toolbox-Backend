package usertickets

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/tickets"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/mailnotify"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/smtp"
	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

func TestTicketBodyLimitsAndOwnerList(t *testing.T) {
	db := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer db.Close()
	for _, id := range []string{"owner", "other"} {
		db.User.Create().SetID(id).SetName(id).SetEmail(id + "@example.test").ExecX(t.Context())
		db.Ticket.Create().SetTicketID(id + "-ticket").SetCreatorUserID(id).SetSubject(id + " fixture").ExecX(t.Context())
	}
	helper := &api.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: db}}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error { c.Locals("userID", "owner"); return c.Next() })
	app.Post("/tickets", handleCreateOwnTicket(helper, tickets.NotificationConfig{}))
	app.Get("/tickets", handleListOwnTickets(helper))
	check := func(method, body, encoding string, want int) string {
		t.Helper()
		req := httptest.NewRequest(method, "/tickets", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Encoding", encoding)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != want {
			t.Fatalf("%s tickets: status %d != %d, err %v", method, resp.StatusCode, want, err)
		}
		return string(data)
	}
	check(http.MethodPost, "{}", "gzip", http.StatusUnsupportedMediaType)
	check(http.MethodPost, strings.Repeat(" ", (64<<10)+1), "identity", http.StatusRequestEntityTooLarge)
	body := `{"subject":"New ticket","message":"Example report","metadata":{"reference":"` + strings.Repeat("a", 16<<10) + `"}}`
	if got := check(http.MethodPost, body, "", http.StatusBadRequest); !strings.Contains(got, "metadata exceeds") {
		t.Fatal("request did not fail at the metadata boundary")
	}
	if got := check(http.MethodGet, "", "", http.StatusOK); !strings.Contains(got, "owner fixture") || strings.Contains(got, "other fixture") {
		t.Fatal("ticket listing did not return only the signed-in user's tickets")
	}
	if got := db.Ticket.Query().CountX(t.Context()); got != 2 {
		t.Fatalf("rejected bodies changed the number of tickets: %d", got)
	}

	helper.SMTPClient = &smtp.HarukiSMTPClient{}
	var reservations []*mailnotify.Reservation
	for reservation := mailnotify.Reserve(); reservation != nil; reservation = mailnotify.Reserve() {
		reservations = append(reservations, reservation)
		defer reservation.Cancel()
	}
	const validTicket = `{"subject":"New ticket","message":"Example report"}`
	check(http.MethodPost, validTicket, "", http.StatusServiceUnavailable)
	if got := db.Ticket.Query().CountX(t.Context()); got != 2 {
		t.Fatalf("admission failure saved a ticket: count = %d", got)
	}
	for _, reservation := range reservations {
		reservation.Cancel()
	}
	check(http.MethodPost, validTicket, "", http.StatusOK)
	if got := db.Ticket.Query().CountX(t.Context()); got != 3 {
		t.Fatalf("retry after admission opened saved %d tickets, want 3 total", got)
	}
}

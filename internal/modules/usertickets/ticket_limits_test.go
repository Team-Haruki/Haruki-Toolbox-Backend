package usertickets

import (
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/tickets"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/mailnotify"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsoncodec"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/smtp"
	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

func TestOwnTicketDetailPagesMessagesAndRejectsForeignCursors(t *testing.T) {
	db := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer db.Close()
	row := db.Ticket.Create().SetTicketID("owned").SetCreatorUserID("owner").SetSubject("fixture").SaveX(t.Context())
	other := db.Ticket.Create().SetTicketID("other").SetCreatorUserID("other").SetSubject("fixture").SaveX(t.Context())
	stamp := time.Now().UTC().Truncate(time.Second)
	fixtures := make([]*postgresql.TicketMessageCreate, 0, 103)
	for range 102 {
		fixtures = append(fixtures, db.TicketMessage.Create().SetTicketID(row.ID).SetMessage("visible").SetCreatedAt(stamp))
	}
	visible := db.TicketMessage.CreateBulk(fixtures...).SaveX(t.Context())
	internal := db.TicketMessage.Create().SetTicketID(row.ID).SetInternal(true).SetMessage("internal note").SaveX(t.Context())
	foreign := db.TicketMessage.Create().SetTicketID(other.ID).SetMessage("foreign reply").SaveX(t.Context())
	helper := &api.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: db}}
	app := fiber.New(fiber.Config{JSONEncoder: jsoncodec.Marshal, JSONDecoder: jsoncodec.Unmarshal})
	app.Use(func(c fiber.Ctx) error { c.Locals("userID", "owner"); return c.Next() })
	app.Get("/tickets/:ticket_id", handleGetOwnTicketDetail(helper))
	read := func(query string, want int, wantCursor bool) userTicketDetailResponse {
		t.Helper()
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/tickets/owned"+query, nil))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("ticket %s: status=%d want=%d", query, resp.StatusCode, want)
		}
		var body struct {
			UpdatedData userTicketDetailResponse `json:"updatedData"`
		}
		if want == http.StatusOK {
			raw, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), `"nextBeforeMessageId":`) != wantCursor {
				t.Fatalf("ticket %s: unexpected continuation field", query)
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
		}
		return body.UpdatedData
	}
	first := read("", http.StatusOK, true)
	if len(first.Messages) != 100 || first.NextBeforeMessageID != visible[2].ID || first.Messages[99].ID != visible[101].ID {
		t.Fatalf("recent page: length=%d cursor=%d", len(first.Messages), first.NextBeforeMessageID)
	}
	second := read(fmt.Sprintf("?before_message_id=%d", first.NextBeforeMessageID), http.StatusOK, false)
	if len(second.Messages) != 2 || second.Messages[0].ID != visible[0].ID || second.Messages[1].ID != visible[1].ID || second.NextBeforeMessageID != 0 {
		t.Fatalf("older page: %#v", second)
	}
	read(fmt.Sprintf("?before_message_id=%d", internal.ID), http.StatusNotFound, false)
	read(fmt.Sprintf("?before_message_id=%d", foreign.ID), http.StatusNotFound, false)
	read("?before_message_id=0", http.StatusBadRequest, false)
}

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
	app.Post("/tickets/:ticket_id/messages", handleAppendOwnTicketMessage(helper, tickets.NotificationConfig{}))
	check := func(method, path, body, encoding string, want int) string {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
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
	check(http.MethodPost, "/tickets", "{}", "gzip", http.StatusUnsupportedMediaType)
	check(http.MethodPost, "/tickets", strings.Repeat(" ", (64<<10)+1), "identity", http.StatusRequestEntityTooLarge)
	body := `{"subject":"New ticket","message":"Example report","metadata":{"reference":"` + strings.Repeat("a", 16<<10) + `"}}`
	if got := check(http.MethodPost, "/tickets", body, "", http.StatusBadRequest); !strings.Contains(got, "metadata exceeds") {
		t.Fatal("request did not fail at the metadata boundary")
	}
	if got := check(http.MethodGet, "/tickets", "", "", http.StatusOK); !strings.Contains(got, "owner fixture") || strings.Contains(got, "other fixture") {
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
	const validReply = `{"message":"Example reply"}`
	check(http.MethodPost, "/tickets", validTicket, "", http.StatusServiceUnavailable)
	check(http.MethodPost, "/tickets/owner-ticket/messages", validReply, "", http.StatusServiceUnavailable)
	if got := db.Ticket.Query().CountX(t.Context()); got != 2 {
		t.Fatalf("admission failure saved a ticket: count = %d", got)
	}
	if got := db.TicketMessage.Query().CountX(t.Context()); got != 0 {
		t.Fatalf("admission failure saved a reply: count = %d", got)
	}
	for _, reservation := range reservations {
		reservation.Cancel()
	}
	check(http.MethodPost, "/tickets", validTicket, "", http.StatusOK)
	check(http.MethodPost, "/tickets/owner-ticket/messages", validReply, "", http.StatusOK)
	if got := db.Ticket.Query().CountX(t.Context()); got != 3 {
		t.Fatalf("retry after admission opened saved %d tickets, want 3 total", got)
	}
	if got := db.TicketMessage.Query().CountX(t.Context()); got != 2 {
		t.Fatalf("expected one initial message and one reply, got %d", got)
	}
}

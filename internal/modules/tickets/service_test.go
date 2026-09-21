package tickets

import (
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticket"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticketmessage"

	_ "github.com/mattn/go-sqlite3"
)

func newTicketServiceTestClient(t *testing.T) *postgresql.Client {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:ticket-service-test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func seedServiceTicket(t *testing.T, client *postgresql.Client, publicID string, status ticket.Status) *postgresql.Ticket {
	t.Helper()
	row, err := client.Ticket.Create().
		SetTicketID(publicID).
		SetCreatorUserID("user-1").
		SetSubject("Need help").
		SetStatus(status).
		Save(t.Context())
	if err != nil {
		t.Fatalf("seed ticket: %v", err)
	}
	return row
}

func TestServiceSharesUserAndAdminMessageTransitions(t *testing.T) {
	client := newTicketServiceTestClient(t)
	service := NewService(client, time.Now)
	row := seedServiceTicket(t, client, "TK-SERVICE-1", ticket.StatusOpen)

	userMessage, err := service.AppendUserMessage(t.Context(), row, "user-1", "user reply")
	if err != nil {
		t.Fatalf("AppendUserMessage: %v", err)
	}
	if userMessage.SenderRole != ticketmessage.SenderRoleUser || userMessage.Internal {
		t.Fatalf("user message = %#v", userMessage)
	}
	row, err = client.Ticket.Get(t.Context(), row.ID)
	if err != nil || row.Status != ticket.StatusPendingAdmin {
		t.Fatalf("status after user reply = %q, %v", row.Status, err)
	}

	adminMessage, err := service.AppendAdminMessage(t.Context(), row, "admin-1", "admin reply", false)
	if err != nil {
		t.Fatalf("AppendAdminMessage: %v", err)
	}
	if adminMessage.SenderRole != ticketmessage.SenderRoleAdmin || adminMessage.Internal {
		t.Fatalf("admin message = %#v", adminMessage)
	}
	row, err = client.Ticket.Get(t.Context(), row.ID)
	if err != nil || row.Status != ticket.StatusPendingUser {
		t.Fatalf("status after admin reply = %q, %v", row.Status, err)
	}

	if _, err := service.AppendAdminMessage(t.Context(), row, "admin-1", "private note", true); err != nil {
		t.Fatalf("AppendAdminMessage internal: %v", err)
	}
	afterInternal, err := client.Ticket.Get(t.Context(), row.ID)
	if err != nil || afterInternal.Status != ticket.StatusPendingUser {
		t.Fatalf("status after internal note = %q, %v", afterInternal.Status, err)
	}
}

func TestServiceUpdateStatusWritesSystemEventAndCloseTimestamp(t *testing.T) {
	client := newTicketServiceTestClient(t)
	fixedNow := time.Date(2026, time.August, 13, 9, 30, 0, 0, time.FixedZone("test", 8*60*60))
	service := NewService(client, func() time.Time { return fixedNow })
	row := seedServiceTicket(t, client, "TK-SERVICE-2", ticket.StatusOpen)

	updated, err := service.UpdateStatus(t.Context(), row, ticket.StatusClosed)
	if err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if updated.ClosedAt == nil || !updated.ClosedAt.Equal(fixedNow.UTC()) {
		t.Fatalf("ClosedAt = %#v, want %v", updated.ClosedAt, fixedNow.UTC())
	}
	messages, err := client.TicketMessage.Query().
		Where(ticketmessage.TicketIDEQ(row.ID)).
		All(t.Context())
	if err != nil {
		t.Fatalf("query system messages: %v", err)
	}
	if len(messages) != 1 || messages[0].SenderRole != ticketmessage.SenderRoleSystem || !messages[0].Internal || messages[0].Message != "Status changed: Open -> Closed" {
		t.Fatalf("system messages = %#v", messages)
	}
}

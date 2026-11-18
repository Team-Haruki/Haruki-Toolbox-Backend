package tickets

import (
	"errors"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticket"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticketmessage"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"

	_ "github.com/mattn/go-sqlite3"
)

func TestTicketRepliesStopAtBudgetWithoutChangingTicket(t *testing.T) {
	client := newTicketServiceTestClient(t)
	service := NewService(client, time.Now)
	row := seedServiceTicket(t, client, "TK-MESSAGE-BUDGET", ticket.StatusOpen)
	fixtures := make([]*postgresql.TicketMessageCreate, 0, maxUserTicketMessages-1)
	for range maxUserTicketMessages - 1 {
		fixtures = append(fixtures, client.TicketMessage.Create().SetTicketID(row.ID).SetSenderRole(ticketmessage.SenderRoleUser).SetMessage("existing"))
	}
	client.TicketMessage.CreateBulk(fixtures...).ExecX(t.Context())
	if _, err := service.AppendUserMessage(t.Context(), row, "user-1", "final allowed reply"); err != nil {
		t.Fatal(err)
	}
	row = client.Ticket.UpdateOneID(row.ID).SetStatus(ticket.StatusResolved).SaveX(t.Context())
	if _, err := service.AppendUserMessage(t.Context(), row, "user-1", "reply over limit"); !errors.Is(err, ErrTicketMessageLimit) {
		t.Fatalf("reply limit error = %v", err)
	}
	if got := client.Ticket.GetX(t.Context(), row.ID); got.Status != ticket.StatusResolved || !got.UpdatedAt.Equal(row.UpdatedAt) {
		t.Fatal("rejected reply changed ticket state")
	}
	if got := row.QueryMessages().CountX(t.Context()); got != maxUserTicketMessages {
		t.Fatalf("message count = %d, want %d", got, maxUserTicketMessages)
	}
	if _, _, err := service.AppendAdminMessage(t.Context(), row, "admin-1", "admin", "support follow-up", false); err != nil {
		t.Fatalf("support cannot reply after the owner's budget: %v", err)
	}
}

func TestAdminTicketWritesRecheckScopeAgainstCurrentParticipation(t *testing.T) {
	client := newTicketServiceTestClient(t)
	service := NewService(client, time.Now)
	client.User.Create().SetID("super-1").SetName("Super").SetEmail("super@example.test").SetRole(userSchema.RoleSuperAdmin).ExecX(t.Context())
	stale := seedServiceTicket(t, client, "TK-PROTECTED-ASSIGNEE", ticket.StatusOpen)
	current := client.Ticket.UpdateOneID(stale.ID).SetAssigneeAdminID("super-1").SaveX(t.Context())
	if _, _, err := service.AppendAdminMessage(t.Context(), stale, "admin-1", "admin", "must be denied", true); !postgresql.IsNotFound(err) {
		t.Fatalf("reply error = %v, want hidden ticket", err)
	}
	if _, err := service.UpdateStatus(t.Context(), stale, "admin-1", "admin", ticket.StatusClosed); !postgresql.IsNotFound(err) {
		t.Fatalf("status error = %v, want hidden ticket", err)
	}
	if got := client.Ticket.GetX(t.Context(), stale.ID); got.Status != current.Status || !got.UpdatedAt.Equal(current.UpdatedAt) {
		t.Fatal("rejected writes changed the ticket")
	}
	if count := stale.QueryMessages().CountX(t.Context()); count != 0 {
		t.Fatalf("rejected writes saved %d replies", count)
	}
}

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

	adminMessage, status, err := service.AppendAdminMessage(t.Context(), row, "admin-1", "admin", "admin reply", false)
	if err != nil {
		t.Fatalf("AppendAdminMessage: %v", err)
	}
	if adminMessage.SenderRole != ticketmessage.SenderRoleAdmin || adminMessage.Internal {
		t.Fatalf("admin message = %#v", adminMessage)
	}
	if status != ticket.StatusPendingUser {
		t.Fatalf("returned status = %q", status)
	}
	row, err = client.Ticket.Get(t.Context(), row.ID)
	if err != nil || row.Status != ticket.StatusPendingUser {
		t.Fatalf("status after admin reply = %q, %v", row.Status, err)
	}

	if _, _, err := service.AppendAdminMessage(t.Context(), row, "admin-1", "admin", "private note", true); err != nil {
		t.Fatalf("AppendAdminMessage internal: %v", err)
	}
	afterInternal, err := client.Ticket.Get(t.Context(), row.ID)
	if err != nil || afterInternal.Status != ticket.StatusPendingUser {
		t.Fatalf("status after internal note = %q, %v", afterInternal.Status, err)
	}
}

func TestAppendAdminMessageReturnsStatusOfTicketClosedSinceRead(t *testing.T) {
	client := newTicketServiceTestClient(t)
	service := NewService(client, time.Now)
	row := seedServiceTicket(t, client, "TK-SERVICE-CLOSED", ticket.StatusOpen)
	if _, err := service.Close(t.Context(), row); err != nil {
		t.Fatal(err)
	}
	message, status, err := service.AppendAdminMessage(t.Context(), row, "admin-1", "admin", "final reply", false)
	if err != nil || status != ticket.StatusClosed || message == nil {
		t.Fatalf("closed ticket reply = %v, %q, %v", message, status, err)
	}
	if got := client.Ticket.GetX(t.Context(), row.ID); got.Status != ticket.StatusClosed || got.ClosedAt == nil {
		t.Fatal("reply changed a closed ticket")
	}
}

func TestServiceUpdateStatusWritesSystemEventAndCloseTimestamp(t *testing.T) {
	client := newTicketServiceTestClient(t)
	fixedNow := time.Date(2026, time.August, 13, 9, 30, 0, 0, time.FixedZone("test", 8*60*60))
	service := NewService(client, func() time.Time { return fixedNow })
	row := seedServiceTicket(t, client, "TK-SERVICE-2", ticket.StatusOpen)

	updated, err := service.UpdateStatus(t.Context(), row, "admin-1", "admin", ticket.StatusClosed)
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

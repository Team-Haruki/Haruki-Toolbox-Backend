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

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
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

func TestAdminTicketPostgresAuthorizationFollowsRowLock(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	client := postgresql.NewClient(postgresql.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT "tickets"\."id" FROM "tickets" WHERE .* FOR UPDATE`).WithArgs(42).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(42))
	mock.ExpectQuery(`SELECT .* FROM "tickets" WHERE .*"protected_users"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()
	tx, err := client.Tx(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := LockAdminTicket(t.Context(), tx, 42, "admin"); !postgresql.IsNotFound(err) {
		t.Fatalf("role-scoped read after reserving the row: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAdminMessagePagesKeepHistoricalVisibilityAndChronology(t *testing.T) {
	client := newTicketServiceTestClient(t)
	client.User.Create().SetID("protected-editor").SetName("Super").SetEmail("editor@example.test").SetRole(userSchema.RoleSuperAdmin).ExecX(t.Context())
	row := seedServiceTicket(t, client, "TK-MESSAGE-PAGE", ticket.StatusOpen)
	stamp := time.Now().UTC()
	fixtures := make([]*postgresql.TicketMessageCreate, 0, maxTicketMessagesPerPage)
	for range maxTicketMessagesPerPage {
		fixtures = append(fixtures, client.TicketMessage.Create().SetTicketID(row.ID).SetMessage("current").SetCreatedAt(stamp))
	}
	current := client.TicketMessage.CreateBulk(fixtures...).SaveX(t.Context())
	historical := client.TicketMessage.Create().SetTicketID(row.ID).SetMessage("earlier").SetCreatedAt(stamp.Add(-time.Minute)).SaveX(t.Context())
	protected := client.TicketMessage.Create().SetTicketID(row.ID).SetSenderUserID("protected-editor").SetInternal(true).SetMessage("protected note").SaveX(t.Context())
	legacy := client.TicketMessage.Create().SetTicketID(row.ID).SetSenderRole(ticketmessage.SenderRoleSystem).SetInternal(true).
		SetMessage(AssignmentEventPrefix + "protected-editor -> Unassigned").SaveX(t.Context())
	byRole := func(role string) *postgresql.TicketMessageQuery {
		return row.QueryMessages().Where(LimitAdminMessageVisibility(role))
	}
	page, cursor, err := ReadMessagePage(t.Context(), byRole("admin"), 0)
	if err != nil || len(page) != maxTicketMessagesPerPage || cursor != current[0].ID || page[0].ID != cursor || page[len(page)-1].ID != current[len(current)-1].ID {
		t.Fatalf("recent page: size=%d cursor=%d err=%v", len(page), cursor, err)
	}
	older, next, err := ReadMessagePage(t.Context(), byRole("admin"), cursor)
	if err != nil || next != 0 || len(older) != 1 || older[0].ID != historical.ID {
		t.Fatalf("earlier page: next=%d rows=%v err=%v", next, older, err)
	}
	for _, hidden := range []int{protected.ID, legacy.ID} {
		if _, _, err := ReadMessagePage(t.Context(), byRole("admin"), hidden); !postgresql.IsNotFound(err) {
			t.Fatalf("hidden message accepted as cursor: %v", err)
		}
		if _, _, err := ReadMessagePage(t.Context(), byRole("super_admin"), hidden); err != nil {
			t.Fatalf("super admin lost access to historical cursor: %v", err)
		}
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

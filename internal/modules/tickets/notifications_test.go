package tickets

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unsafe"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticket"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"

	_ "github.com/mattn/go-sqlite3"
)

type recordingMailSender struct {
	calls []recordedMail
	err   error
}

type recordedMail struct {
	to          []string
	subject     string
	body        string
	displayName string
}

func (s *recordingMailSender) Send(to []string, subject, body, displayName string) error {
	s.calls = append(s.calls, recordedMail{append([]string(nil), to...), subject, body, displayName})
	return s.err
}

func newTicketNotificationTestClient(t *testing.T) *postgresql.Client {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:ticket-notification-domain-test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func seedTicketNotificationUser(t *testing.T, client *postgresql.Client, id, email string, role userSchema.Role, enabled, banned bool) {
	t.Helper()
	if _, err := client.User.Create().SetID(id).SetName(id).SetEmail(email).SetRole(role).
		SetTicketEmailNotificationsEnabled(enabled).SetBanned(banned).Save(t.Context()); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func TestBuildEventUsesInjectedConfigAndClonesAsyncStrings(t *testing.T) {
	actorBytes := []byte("actor-1")
	messageBytes := []byte("request message")
	publicIDBytes := []byte("TK-CLONE")
	actor := unsafe.String(unsafe.SliceData(actorBytes), len(actorBytes))
	message := unsafe.String(unsafe.SliceData(messageBytes), len(messageBytes))
	publicID := unsafe.String(unsafe.SliceData(publicIDBytes), len(publicIDBytes))

	event := BuildEvent(&postgresql.Ticket{
		TicketID:      publicID,
		CreatorUserID: "user-1",
		Subject:       "Subject",
		Status:        ticket.StatusOpen,
	}, actor, message, &recordingMailSender{}, NotificationConfig{
		FrontendURL: "https://injected.example",
		DetailPath:  "/support/tickets",
		DisplayName: "Injected Sender",
	})

	actorBytes[0] = 'X'
	messageBytes[0] = 'X'
	publicIDBytes[0] = 'X'
	if event.ActorUserID != "actor-1" || event.Message != "request message" || event.Ticket.PublicID != "TK-CLONE" {
		t.Fatalf("event retained aliased request strings: %#v", event)
	}
	if event.FrontendURL != "https://injected.example" || event.DetailPath != "/support/tickets" || event.DisplayName != "Injected Sender" {
		t.Fatalf("notification config = %#v", event)
	}
}

func TestNotifyAdminsOfNewTicketSelectsEnabledAdmins(t *testing.T) {
	client := newTicketNotificationTestClient(t)
	seedTicketNotificationUser(t, client, "creator", "creator@example.com", userSchema.RoleUser, false, false)
	seedTicketNotificationUser(t, client, "admin-1", "Admin1@Example.com", userSchema.RoleAdmin, true, false)
	seedTicketNotificationUser(t, client, "admin-2", "admin2@example.com", userSchema.RoleSuperAdmin, true, false)
	seedTicketNotificationUser(t, client, "admin-off", "off@example.com", userSchema.RoleAdmin, false, false)
	seedTicketNotificationUser(t, client, "admin-banned", "banned@example.com", userSchema.RoleSuperAdmin, true, true)

	sender := &recordingMailSender{}
	NotifyAdminsOfNewTicket(context.Background(), client, Event{
		Ticket:      TicketContext{PublicID: "TK-1", CreatorUserID: "creator", Subject: "Upload failed", Status: ticket.StatusOpen},
		ActorUserID: "creator",
		Message:     "first line\nsecond line",
		FrontendURL: "https://haruki.example",
		DetailPath:  "/tickets",
		MailSender:  sender,
		DisplayName: "Haruki",
	})
	if len(sender.calls) != 1 || strings.Join(sender.calls[0].to, ",") != "admin1@example.com,admin2@example.com" {
		t.Fatalf("mail calls = %#v", sender.calls)
	}
	if !strings.Contains(sender.calls[0].body, "https://haruki.example/tickets/TK-1") {
		t.Fatalf("body = %q", sender.calls[0].body)
	}
}

func TestNotifyAdminsOfUserReplySelectsEnabledAdmins(t *testing.T) {
	client := newTicketNotificationTestClient(t)
	seedTicketNotificationUser(t, client, "creator", "creator@example.com", userSchema.RoleUser, false, false)
	seedTicketNotificationUser(t, client, "assigned-admin", "assigned@example.com", userSchema.RoleAdmin, true, false)
	seedTicketNotificationUser(t, client, "other-admin", "other@example.com", userSchema.RoleAdmin, true, false)
	seedTicketNotificationUser(t, client, "admin-off", "off@example.com", userSchema.RoleAdmin, false, false)

	sender := &recordingMailSender{}
	NotifyAdminsOfUserReply(context.Background(), client, Event{
		Ticket: TicketContext{
			PublicID:        "TK-2",
			CreatorUserID:   "creator",
			Subject:         "Need help",
			Status:          ticket.StatusPendingAdmin,
			AssigneeAdminID: "assigned-admin",
		},
		ActorUserID: "creator",
		Message:     "hello",
		FrontendURL: "https://haruki.example",
		MailSender:  sender,
	})
	if len(sender.calls) != 1 || strings.Join(sender.calls[0].to, ",") != "assigned@example.com,other@example.com" {
		t.Fatalf("mail calls = %#v", sender.calls)
	}
}

func TestNotifyAdminsOfUserReplySkipsDisabledAssignee(t *testing.T) {
	client := newTicketNotificationTestClient(t)
	seedTicketNotificationUser(t, client, "creator", "creator@example.com", userSchema.RoleUser, false, false)
	seedTicketNotificationUser(t, client, "assigned-admin", "assigned@example.com", userSchema.RoleAdmin, false, false)
	seedTicketNotificationUser(t, client, "other-admin", "other@example.com", userSchema.RoleAdmin, true, false)

	sender := &recordingMailSender{}
	NotifyAdminsOfUserReply(context.Background(), client, Event{
		Ticket:      TicketContext{PublicID: "TK-3", CreatorUserID: "creator", Status: ticket.StatusPendingAdmin, AssigneeAdminID: "assigned-admin"},
		ActorUserID: "creator",
		Message:     "hello",
		MailSender:  sender,
	})
	if len(sender.calls) != 1 || strings.Join(sender.calls[0].to, ",") != "other@example.com" {
		t.Fatalf("mail calls = %#v", sender.calls)
	}
}

func TestNotifyUserOfAdminReplySendsEscapedMailToCreator(t *testing.T) {
	client := newTicketNotificationTestClient(t)
	seedTicketNotificationUser(t, client, "creator", "Creator@Example.com", userSchema.RoleUser, false, false)
	seedTicketNotificationUser(t, client, "admin-1", "admin@example.com", userSchema.RoleAdmin, true, false)

	sender := &recordingMailSender{}
	NotifyUserOfAdminReply(context.Background(), client, Event{
		Ticket:      TicketContext{PublicID: "TK-4", CreatorUserID: "creator", Subject: "Question", Status: ticket.StatusPendingUser},
		ActorUserID: "admin-1",
		Message:     " fixed <b>maybe</b> ",
		FrontendURL: "https://haruki.example/",
		MailSender:  sender,
	})
	if len(sender.calls) != 1 || strings.Join(sender.calls[0].to, ",") != "creator@example.com" {
		t.Fatalf("mail calls = %#v", sender.calls)
	}
	if !strings.Contains(sender.calls[0].body, "fixed &lt;b&gt;maybe&lt;/b&gt;") {
		t.Fatalf("body = %q", sender.calls[0].body)
	}
}

func TestTicketNotificationSendFailureDoesNotPanic(t *testing.T) {
	client := newTicketNotificationTestClient(t)
	seedTicketNotificationUser(t, client, "creator", "creator@example.com", userSchema.RoleUser, false, false)
	seedTicketNotificationUser(t, client, "admin-1", "admin@example.com", userSchema.RoleAdmin, true, false)
	sender := &recordingMailSender{err: errors.New("smtp down")}
	NotifyAdminsOfNewTicket(context.Background(), client, Event{
		Ticket:      TicketContext{PublicID: "TK-5", CreatorUserID: "creator", Status: ticket.StatusOpen},
		ActorUserID: "creator",
		MailSender:  sender,
	})
	if len(sender.calls) != 1 {
		t.Fatalf("mail calls = %d, want 1", len(sender.calls))
	}
}

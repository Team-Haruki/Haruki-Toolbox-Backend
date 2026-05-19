package ticketnotifications

import (
	"context"
	"errors"
	"haruki-suite/config"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/enttest"
	"haruki-suite/utils/database/postgresql/ticket"
	userSchema "haruki-suite/utils/database/postgresql/user"
	"strings"
	"testing"

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

func (s *recordingMailSender) Send(to []string, subject, body string, displayName string) error {
	s.calls = append(s.calls, recordedMail{
		to:          append([]string(nil), to...),
		subject:     subject,
		body:        body,
		displayName: displayName,
	})
	return s.err
}

func newTicketNotificationTestClient(t *testing.T) *postgresql.Client {
	t.Helper()

	client := enttest.Open(t, "sqlite3", "file:ticket-notification-test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() {
		_ = client.Close()
	})
	return client
}

func seedTicketNotificationUser(t *testing.T, client *postgresql.Client, id string, email string, role userSchema.Role, enabled bool, banned bool) {
	t.Helper()

	if _, err := client.User.Create().
		SetID(id).
		SetName(id).
		SetEmail(email).
		SetRole(role).
		SetTicketEmailNotificationsEnabled(enabled).
		SetBanned(banned).
		Save(t.Context()); err != nil {
		t.Fatalf("failed to seed user %s: %v", id, err)
	}
}

func TestNotifyAdminsOfNewTicketSelectsEnabledAdminsOnly(t *testing.T) {
	client := newTicketNotificationTestClient(t)
	seedTicketNotificationUser(t, client, "creator", "creator@example.com", userSchema.RoleUser, false, false)
	seedTicketNotificationUser(t, client, "admin-1", "Admin1@Example.com", userSchema.RoleAdmin, true, false)
	seedTicketNotificationUser(t, client, "admin-2", "admin2@example.com", userSchema.RoleSuperAdmin, true, false)
	seedTicketNotificationUser(t, client, "admin-off", "off@example.com", userSchema.RoleAdmin, false, false)
	seedTicketNotificationUser(t, client, "admin-banned", "banned@example.com", userSchema.RoleAdmin, true, true)
	seedTicketNotificationUser(t, client, "plain-user", "plain@example.com", userSchema.RoleUser, true, false)

	sender := &recordingMailSender{}
	NotifyAdminsOfNewTicket(context.Background(), client, Event{
		Ticket: TicketContext{
			PublicID:      "TK-1",
			CreatorUserID: "creator",
			Subject:       "Upload failed",
			Status:        ticket.StatusOpen,
		},
		ActorUserID: "creator",
		Message:     " first line\nsecond line ",
		FrontendURL: "https://haruki.example",
		MailSender:  sender,
		DisplayName: "Haruki",
	})

	if len(sender.calls) != 1 {
		t.Fatalf("mail calls = %d, want 1", len(sender.calls))
	}
	got := strings.Join(sender.calls[0].to, ",")
	if got != "admin1@example.com,admin2@example.com" {
		t.Fatalf("recipients = %q, want enabled admins only", got)
	}
	if !strings.Contains(sender.calls[0].subject, "新工单") || !strings.Contains(sender.calls[0].subject, "TK-1") {
		t.Fatalf("subject = %q, want action and ticket id", sender.calls[0].subject)
	}
	if !strings.Contains(sender.calls[0].body, "https://haruki.example/tickets/TK-1") {
		t.Fatalf("body = %q, want ticket link", sender.calls[0].body)
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

	if len(sender.calls) != 1 {
		t.Fatalf("mail calls = %d, want 1", len(sender.calls))
	}
	if got := strings.Join(sender.calls[0].to, ","); got != "assigned@example.com,other@example.com" {
		t.Fatalf("recipients = %q, want enabled admins only", got)
	}
}

func TestNotifyAdminsOfUserReplyFallsBackWhenAssignedAdminDisabled(t *testing.T) {
	client := newTicketNotificationTestClient(t)
	seedTicketNotificationUser(t, client, "creator", "creator@example.com", userSchema.RoleUser, false, false)
	seedTicketNotificationUser(t, client, "assigned-admin", "assigned@example.com", userSchema.RoleAdmin, false, false)
	seedTicketNotificationUser(t, client, "other-admin", "other@example.com", userSchema.RoleAdmin, true, false)

	sender := &recordingMailSender{}
	NotifyAdminsOfUserReply(context.Background(), client, Event{
		Ticket: TicketContext{
			PublicID:        "TK-3",
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

	if len(sender.calls) != 1 {
		t.Fatalf("mail calls = %d, want 1", len(sender.calls))
	}
	if got := strings.Join(sender.calls[0].to, ","); got != "other@example.com" {
		t.Fatalf("recipients = %q, want fallback enabled admin", got)
	}
}

func TestNotifyUserOfAdminReplySendsToTicketCreator(t *testing.T) {
	client := newTicketNotificationTestClient(t)
	seedTicketNotificationUser(t, client, "creator", "Creator@Example.com", userSchema.RoleUser, false, false)
	seedTicketNotificationUser(t, client, "admin-1", "admin@example.com", userSchema.RoleAdmin, true, false)

	sender := &recordingMailSender{}
	NotifyUserOfAdminReply(context.Background(), client, Event{
		Ticket: TicketContext{
			PublicID:      "TK-4",
			CreatorUserID: "creator",
			Subject:       "Question",
			Status:        ticket.StatusPendingUser,
		},
		ActorUserID: "admin-1",
		Message:     "  fixed <b>maybe</b>  ",
		FrontendURL: "https://haruki.example/",
		MailSender:  sender,
	})

	if len(sender.calls) != 1 {
		t.Fatalf("mail calls = %d, want 1", len(sender.calls))
	}
	if got := strings.Join(sender.calls[0].to, ","); got != "creator@example.com" {
		t.Fatalf("recipients = %q, want creator email", got)
	}
	if !strings.Contains(sender.calls[0].body, "fixed &lt;b&gt;maybe&lt;/b&gt;") {
		t.Fatalf("body = %q, want escaped preview", sender.calls[0].body)
	}
}

func TestTicketNotificationSendFailureDoesNotPanic(t *testing.T) {
	client := newTicketNotificationTestClient(t)
	seedTicketNotificationUser(t, client, "creator", "creator@example.com", userSchema.RoleUser, false, false)
	seedTicketNotificationUser(t, client, "admin-1", "admin@example.com", userSchema.RoleAdmin, true, false)

	sender := &recordingMailSender{err: errors.New("smtp down")}
	NotifyAdminsOfNewTicket(context.Background(), client, Event{
		Ticket: TicketContext{
			PublicID:      "TK-5",
			CreatorUserID: "creator",
			Subject:       "Question",
			Status:        ticket.StatusOpen,
		},
		ActorUserID: "creator",
		Message:     "hello",
		FrontendURL: "https://haruki.example",
		MailSender:  sender,
	})

	if len(sender.calls) != 1 {
		t.Fatalf("mail calls = %d, want 1", len(sender.calls))
	}
}

func TestBuildEventUsesGlobalFrontendAndSMTPName(t *testing.T) {
	oldCfg := config.Cfg
	t.Cleanup(func() {
		config.Cfg = oldCfg
	})
	config.Cfg.UserSystem.FrontendURL = "https://global.example"
	config.Cfg.UserSystem.SMTP.MailName = "Global Sender"

	assigneeID := "admin-1"
	event := BuildEvent(&postgresql.Ticket{
		ID:              7,
		TicketID:        "TK-6",
		CreatorUserID:   "creator",
		Subject:         "Global subject",
		Status:          ticket.StatusPendingAdmin,
		AssigneeAdminID: &assigneeID,
	}, "creator", "hello", &recordingMailSender{})

	if event.FrontendURL != "https://global.example" {
		t.Fatalf("FrontendURL = %q, want global config", event.FrontendURL)
	}
	if event.DisplayName != "Global Sender" {
		t.Fatalf("DisplayName = %q, want global config", event.DisplayName)
	}
	if event.Ticket.AssigneeAdminID != "admin-1" {
		t.Fatalf("AssigneeAdminID = %q, want admin-1", event.Ticket.AssigneeAdminID)
	}
}

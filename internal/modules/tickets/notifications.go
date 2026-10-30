package tickets

import (
	"context"
	"net/url"
	"strings"
	"unicode/utf8"

	platformIdentity "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/identity"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/mailtemplate"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/ticket"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	harukiSMTP "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/smtp"

	sql "entgo.io/ent/dialect/sql"
)

const ticketMailPreviewLength = 240

// MailSender is the outbound mail port required by ticket notifications. An
// empty displayName means the configured sender name.
type MailSender interface {
	SendMessage(to []string, msg harukiSMTP.Message, displayName string) error
}

// NotificationConfig is supplied by the composition/transport layer. Keeping
// it in the event makes background jobs independent of mutable global config.
type NotificationConfig struct {
	FrontendURL string
	DetailPath  string
	DisplayName string
}

type TicketContext struct {
	InternalID      int
	PublicID        string
	CreatorUserID   string
	Subject         string
	Status          ticket.Status
	AssigneeAdminID string
}

type Event struct {
	Ticket      TicketContext
	ActorUserID string
	Message     string
	FrontendURL string
	DetailPath  string
	MailSender  MailSender
	DisplayName string
}

func NotifyAdminsOfNewTicket(ctx context.Context, db *postgresql.Client, event Event) {
	notifyAdmins(ctx, db, event, mailtemplate.TicketCreated)
}

func NotifyAdminsOfUserReply(ctx context.Context, db *postgresql.Client, event Event) {
	notifyAdmins(ctx, db, event, mailtemplate.TicketUserReplied)
}

func NotifyUserOfAdminReply(ctx context.Context, db *postgresql.Client, event Event) {
	if db == nil || event.MailSender == nil {
		return
	}

	creatorUserID := strings.TrimSpace(event.Ticket.CreatorUserID)
	if creatorUserID == "" || sameUserID(creatorUserID, event.ActorUserID) {
		return
	}

	recipient, err := db.User.Query().
		Where(userSchema.IDEQ(creatorUserID), userSchema.BannedEQ(false)).
		Select(userSchema.FieldID, userSchema.FieldEmail).
		Only(ctx)
	if err != nil {
		if !postgresql.IsNotFound(err) {
			harukiLogger.Warnf("Failed to query ticket user notification recipient for ticket %s: %v", event.Ticket.PublicID, err)
		}
		return
	}

	email := platformIdentity.NormalizeEmail(recipient.Email)
	if email == "" || !strings.Contains(email, "@") {
		return
	}
	sendTicketMail(event, []string{email}, mailtemplate.TicketAdminReplied)
}

func notifyAdmins(ctx context.Context, db *postgresql.Client, event Event, kind mailtemplate.TicketEvent) {
	if db == nil || event.MailSender == nil {
		return
	}

	query := db.User.Query().
		Where(
			userSchema.TicketEmailNotificationsEnabledEQ(true),
			userSchema.BannedEQ(false),
			userSchema.RoleIn(userSchema.RoleAdmin, userSchema.RoleSuperAdmin),
		)
	if actorUserID := strings.TrimSpace(event.ActorUserID); actorUserID != "" {
		query = query.Where(userSchema.IDNEQ(actorUserID))
	}

	rows, err := query.
		Order(userSchema.ByID(sql.OrderAsc())).
		Select(userSchema.FieldID, userSchema.FieldEmail).
		All(ctx)
	if err != nil {
		harukiLogger.Warnf("Failed to query admin ticket notification recipients for ticket %s: %v", event.Ticket.PublicID, err)
		return
	}

	recipients := normalizeRecipientEmails(rows)
	if len(recipients) == 0 {
		return
	}
	sendTicketMail(event, recipients, kind)
}

func normalizeRecipientEmails(rows []*postgresql.User) []string {
	recipients := make([]string, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		email := platformIdentity.NormalizeEmail(row.Email)
		if email == "" || !strings.Contains(email, "@") {
			continue
		}
		if _, ok := seen[email]; ok {
			continue
		}
		seen[email] = struct{}{}
		recipients = append(recipients, email)
	}
	return recipients
}

func sendTicketMail(event Event, recipients []string, kind mailtemplate.TicketEvent) {
	if len(recipients) == 0 || event.MailSender == nil {
		return
	}

	msg, err := mailtemplate.Render(buildTicketMail(event, kind))
	if err != nil {
		harukiLogger.Warnf("Failed to render ticket notification mail for ticket %s: %v", event.Ticket.PublicID, err)
		return
	}
	if err := event.MailSender.SendMessage(recipients, msg, strings.TrimSpace(event.DisplayName)); err != nil {
		harukiLogger.Warnf("Failed to send ticket notification mail for ticket %s to %s: %v", event.Ticket.PublicID, strings.Join(recipients, ","), err)
	}
}

// BuildEvent clones every request-derived string it stores. Events are sent by
// a background worker after Fiber may have recycled request buffers.
func BuildEvent(ticketRow *postgresql.Ticket, actorUserID, message string, mailSender MailSender, cfg NotificationConfig) Event {
	actor := strings.Clone(strings.TrimSpace(actorUserID))
	message = strings.Clone(message)
	frontendURL := strings.Clone(cfg.FrontendURL)
	detailPath := strings.Clone(strings.TrimSpace(cfg.DetailPath))
	if detailPath == "" {
		detailPath = "/tickets"
	}
	displayName := strings.Clone(strings.TrimSpace(cfg.DisplayName))
	if ticketRow == nil {
		return Event{
			ActorUserID: actor,
			Message:     message,
			MailSender:  mailSender,
			FrontendURL: frontendURL,
			DetailPath:  detailPath,
			DisplayName: displayName,
		}
	}
	return Event{
		Ticket: TicketContext{
			InternalID:      ticketRow.ID,
			PublicID:        strings.Clone(ticketRow.TicketID),
			CreatorUserID:   strings.Clone(ticketRow.CreatorUserID),
			Subject:         strings.Clone(ticketRow.Subject),
			Status:          ticketRow.Status,
			AssigneeAdminID: strings.Clone(ticketAssigneeAdminID(ticketRow)),
		},
		ActorUserID: actor,
		Message:     message,
		FrontendURL: frontendURL,
		DetailPath:  detailPath,
		MailSender:  mailSender,
		DisplayName: displayName,
	}
}

func buildTicketMail(event Event, kind mailtemplate.TicketEvent) mailtemplate.Email {
	return mailtemplate.TicketNotification(mailtemplate.Ticket{
		Event:    kind,
		PublicID: event.Ticket.PublicID,
		Subject:  event.Ticket.Subject,
		Status:   ticketStatusLabel(event.Ticket.Status),
		Preview:  normalizeMessagePreview(event.Message, ticketMailPreviewLength),
		URL:      buildTicketDetailURL(event.FrontendURL, event.DetailPath, event.Ticket.PublicID),
	})
}

func buildTicketDetailURL(frontendURL, detailPath, publicTicketID string) string {
	frontendURL = strings.TrimRight(strings.TrimSpace(frontendURL), "/")
	detailPath = strings.TrimSpace(detailPath)
	publicTicketID = strings.TrimSpace(publicTicketID)
	if frontendURL == "" || publicTicketID == "" {
		return ""
	}
	if detailPath == "" {
		detailPath = "/tickets"
	}
	if !strings.HasPrefix(detailPath, "/") {
		detailPath = "/" + detailPath
	}
	detailPath = strings.TrimRight(detailPath, "/")
	return frontendURL + detailPath + "/" + url.PathEscape(publicTicketID)
}

func normalizeMessagePreview(raw string, maxRunes int) string {
	trimmed := strings.TrimSpace(strings.Join(strings.Fields(raw), " "))
	if trimmed == "" {
		return ""
	}
	return truncateRunes(trimmed, maxRunes)
}

func truncateRunes(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	if maxRunes == 1 {
		return string(runes[:1])
	}
	return string(runes[:maxRunes-1]) + "…"
}

func sameUserID(a, b string) bool {
	return strings.TrimSpace(a) != "" && strings.TrimSpace(a) == strings.TrimSpace(b)
}

func ticketAssigneeAdminID(ticketRow *postgresql.Ticket) string {
	if ticketRow == nil || ticketRow.AssigneeAdminID == nil {
		return ""
	}
	return strings.TrimSpace(*ticketRow.AssigneeAdminID)
}

func ticketStatusLabel(status ticket.Status) string {
	switch status {
	case ticket.StatusOpen:
		return "新建"
	case ticket.StatusPendingAdmin:
		return "待管理员处理"
	case ticket.StatusPendingUser:
		return "待用户回复"
	case ticket.StatusResolved:
		return "已解决"
	case ticket.StatusClosed:
		return "已关闭"
	default:
		return string(status)
	}
}

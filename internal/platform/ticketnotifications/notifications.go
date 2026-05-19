package ticketnotifications

import (
	"context"
	"fmt"
	"haruki-suite/config"
	platformIdentity "haruki-suite/internal/platform/identity"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/ticket"
	userSchema "haruki-suite/utils/database/postgresql/user"
	harukiLogger "haruki-suite/utils/logger"
	"html"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	defaultTicketMailDisplayName = "Haruki Toolbox"
	ticketMailPreviewLength      = 240
)

type MailSender interface {
	Send(to []string, subject, body string, displayName string) error
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
	notifyAdmins(ctx, db, event, "新工单", false)
}

func NotifyAdminsOfUserReply(ctx context.Context, db *postgresql.Client, event Event) {
	notifyAdmins(ctx, db, event, "用户回复", true)
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
	sendTicketMail(event, []string{email}, "工单有新回复")
}

func notifyAdmins(ctx context.Context, db *postgresql.Client, event Event, action string, preferAssignee bool) {
	if db == nil || event.MailSender == nil {
		return
	}

	if preferAssignee {
		if recipients := queryPreferredAssigneeRecipients(ctx, db, event); len(recipients) > 0 {
			sendTicketMail(event, recipients, action)
			return
		}
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

	rows, err := query.Select(userSchema.FieldID, userSchema.FieldEmail).All(ctx)
	if err != nil {
		harukiLogger.Warnf("Failed to query admin ticket notification recipients for ticket %s: %v", event.Ticket.PublicID, err)
		return
	}

	recipients := normalizeRecipientEmails(rows)
	if len(recipients) == 0 {
		return
	}
	sendTicketMail(event, recipients, action)
}

func queryPreferredAssigneeRecipients(ctx context.Context, db *postgresql.Client, event Event) []string {
	assigneeAdminID := strings.TrimSpace(event.Ticket.AssigneeAdminID)
	if assigneeAdminID == "" || sameUserID(assigneeAdminID, event.ActorUserID) {
		return nil
	}

	row, err := db.User.Query().
		Where(
			userSchema.IDEQ(assigneeAdminID),
			userSchema.TicketEmailNotificationsEnabledEQ(true),
			userSchema.BannedEQ(false),
			userSchema.RoleIn(userSchema.RoleAdmin, userSchema.RoleSuperAdmin),
		).
		Select(userSchema.FieldID, userSchema.FieldEmail).
		Only(ctx)
	if err != nil {
		if !postgresql.IsNotFound(err) {
			harukiLogger.Warnf("Failed to query assigned admin ticket notification recipient for ticket %s: %v", event.Ticket.PublicID, err)
		}
		return nil
	}
	return normalizeRecipientEmails([]*postgresql.User{row})
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

func sendTicketMail(event Event, recipients []string, action string) {
	if len(recipients) == 0 || event.MailSender == nil {
		return
	}

	subject := buildTicketMailSubject(event.Ticket, action)
	body := buildTicketMailBody(event)
	displayName := strings.TrimSpace(event.DisplayName)
	if displayName == "" {
		displayName = defaultTicketMailDisplayName
	}
	if err := event.MailSender.Send(recipients, subject, body, displayName); err != nil {
		harukiLogger.Warnf("Failed to send ticket notification mail for ticket %s to %s: %v", event.Ticket.PublicID, strings.Join(recipients, ","), err)
	}
}

func BuildEvent(ticketRow *postgresql.Ticket, actorUserID string, message string, mailSender MailSender) Event {
	if ticketRow == nil {
		return Event{ActorUserID: strings.TrimSpace(actorUserID), Message: message, MailSender: mailSender, FrontendURL: config.Cfg.UserSystem.FrontendURL, DetailPath: "/tickets", DisplayName: config.Cfg.UserSystem.SMTP.MailName}
	}
	return Event{
		Ticket: TicketContext{
			InternalID:      ticketRow.ID,
			PublicID:        ticketRow.TicketID,
			CreatorUserID:   ticketRow.CreatorUserID,
			Subject:         ticketRow.Subject,
			Status:          ticketRow.Status,
			AssigneeAdminID: ticketAssigneeAdminID(ticketRow),
		},
		ActorUserID: strings.TrimSpace(actorUserID),
		Message:     message,
		FrontendURL: config.Cfg.UserSystem.FrontendURL,
		DetailPath:  "/tickets",
		MailSender:  mailSender,
		DisplayName: strings.TrimSpace(config.Cfg.UserSystem.SMTP.MailName),
	}
}

func buildTicketMailSubject(ticketCtx TicketContext, action string) string {
	action = strings.TrimSpace(action)
	if action == "" {
		action = "工单通知"
	}
	publicID := strings.TrimSpace(ticketCtx.PublicID)
	subject := strings.TrimSpace(ticketCtx.Subject)
	switch {
	case publicID != "" && subject != "":
		return fmt.Sprintf("[Haruki Toolbox] %s：%s %s", action, publicID, truncateRunes(subject, 60))
	case publicID != "":
		return fmt.Sprintf("[Haruki Toolbox] %s：%s", action, publicID)
	case subject != "":
		return fmt.Sprintf("[Haruki Toolbox] %s：%s", action, truncateRunes(subject, 60))
	default:
		return "[Haruki Toolbox] " + action
	}
}

func buildTicketMailBody(event Event) string {
	detailURL := buildTicketDetailURL(event.FrontendURL, event.DetailPath, event.Ticket.PublicID)
	preview := normalizeMessagePreview(event.Message, ticketMailPreviewLength)

	var builder strings.Builder
	builder.WriteString("<p>Haruki Toolbox 工单有新的动态。</p>")
	builder.WriteString("<ul>")
	writeTicketMailListItem(&builder, "工单编号", event.Ticket.PublicID)
	writeTicketMailListItem(&builder, "工单主题", event.Ticket.Subject)
	writeTicketMailListItem(&builder, "当前状态", ticketStatusLabel(event.Ticket.Status))
	if preview != "" {
		writeTicketMailListItem(&builder, "内容摘要", preview)
	}
	builder.WriteString("</ul>")
	if detailURL != "" {
		builder.WriteString(`<p><a href="`)
		builder.WriteString(html.EscapeString(detailURL))
		builder.WriteString(`">打开工单详情</a></p>`)
	}
	builder.WriteString("<p>这是一封自动通知邮件，请不要直接回复。</p>")
	return builder.String()
}

func writeTicketMailListItem(builder *strings.Builder, label string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	builder.WriteString("<li><strong>")
	builder.WriteString(html.EscapeString(label))
	builder.WriteString("：</strong>")
	builder.WriteString(html.EscapeString(value))
	builder.WriteString("</li>")
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

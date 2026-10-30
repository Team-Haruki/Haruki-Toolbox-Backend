package mailtemplate

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// This file holds the content of every email the backend sends. Each
// constructor returns data only; the shared layout draws it.

const ignoreIfNotYou = "如果这不是您本人的操作，请忽略此邮件，您的账号不会发生任何变化。请勿将验证码告诉任何人。"

// codeNote states a code's lifetime in whole minutes, rounded down so the
// mail never promises more time than the code has.
func codeNote(ttl time.Duration) string {
	if minutes := int(ttl / time.Minute); minutes >= 1 {
		return fmt.Sprintf("%d 分钟内有效", minutes)
	}
	return "1 分钟内有效"
}

// EmailVerificationCode is the one-time code that proves the recipient owns
// an email address (used when binding a QQ account).
func EmailVerificationCode(code string, ttl time.Duration) Email {
	return Email{
		Subject:   subject("邮箱验证码"),
		Preheader: "您的邮箱验证码，" + codeNote(ttl) + "。",
		Title:     "验证您的邮箱",
		Intro:     []string{"您正在 Haruki 工具箱中验证此邮箱地址。请在页面中输入下方验证码完成验证。"},
		Code:      &Code{Label: "验证码", Value: code, Note: codeNote(ttl)},
		Notice:    ignoreIfNotYou,
	}
}

// BotVerificationCode is the one-time code for registering a HarukiBot NEO
// account or resetting its credential.
func BotVerificationCode(code string, ttl time.Duration) Email {
	return Email{
		Subject:   subject("HarukiBot NEO 验证码"),
		Preheader: "HarukiBot NEO 验证码，" + codeNote(ttl) + "。",
		Title:     "验证您的 QQ 邮箱",
		Intro:     []string{"您正在 Haruki 工具箱中注册 HarukiBot NEO 或重置其凭据。请在页面中输入下方验证码继续。"},
		Code:      &Code{Label: "验证码", Value: code, Note: codeNote(ttl)},
		Notice:    ignoreIfNotYou,
	}
}

// GameAccountTransferred tells the previous owner that a game account
// binding moved to another Toolbox account after an in-game ownership check.
func GameAccountTransferred(server, gameUserID string, at time.Time) Email {
	return Email{
		Subject: subject("游戏账号绑定已转移"),
		Title:   "游戏账号绑定已转移",
		Intro: []string{
			"您在 Haruki 工具箱绑定的一个游戏账号，已通过游戏内归属验证转移到另一个工具箱账号。您的工具箱账号不再绑定该游戏账号。",
		},
		Details: []Detail{
			{Label: "服务器", Value: strings.ToUpper(strings.TrimSpace(server))},
			{Label: "游戏账号 ID", Value: strings.TrimSpace(gameUserID)},
			{Label: "时间", Value: FormatTime(at)},
		},
		Notice: "如果这不是您预期的变更，请尽快检查该游戏账号的安全状况（例如在游戏内重新生成引继码），并通过工单联系管理员。",
	}
}

// InheritVaultActivity describes one access to a stored transfer code. It
// never carries the code, its password or any credential.
type InheritVaultActivity struct {
	Server           string
	MaskedGameUserID string
	IP               string
	At               time.Time
}

// details leaves out any value that is unknown, so a security notice is
// never dropped for lack of an optional field.
func (a InheritVaultActivity) details(action string) []Detail {
	return nonEmptyDetails(
		Detail{Label: "操作", Value: action},
		Detail{Label: "服务器", Value: strings.ToUpper(strings.TrimSpace(a.Server))},
		Detail{Label: "游戏账号", Value: strings.TrimSpace(a.MaskedGameUserID)},
		Detail{Label: "时间", Value: FormatTime(a.At)},
		Detail{Label: "IP 地址", Value: strings.TrimSpace(a.IP)},
	)
}

func nonEmptyDetails(all ...Detail) []Detail {
	var details []Detail
	for _, d := range all {
		if d.Value != "" {
			details = append(details, d)
		}
	}
	return details
}

const inheritNotYou = "如果这不是您本人的操作，请立即修改 Haruki 工具箱密码（如设置了保险库 PIN，请一并更换），在保险库中销毁该条目，并在游戏内重新生成引继码，使原引继码失效。"

// InheritCodeRevealed tells the owner that a transfer code stored in their
// inherit vault was shown.
func InheritCodeRevealed(a InheritVaultActivity) Email {
	return Email{
		Subject: subject("保险库中的引继码已被查看"),
		Title:   "引继码已被查看",
		Intro:   []string{"您的 Haruki 工具箱引继保险库中，有一条引继码刚刚被查看。"},
		Details: a.details("查看引继码"),
		Notice:  inheritNotYou,
	}
}

// InheritCodeReissued tells the owner that a new transfer code was issued
// from their inherit vault, which invalidates the previous one.
func InheritCodeReissued(a InheritVaultActivity) Email {
	return Email{
		Subject: subject("保险库中的引继码已重新签发"),
		Title:   "引继码已重新签发",
		Intro: []string{
			"您的 Haruki 工具箱引继保险库刚刚为一个游戏账号签发了新的引继码，原引继码已失效。新引继码只能在保险库中查看。",
		},
		Details: a.details("重新签发引继码"),
		Notice:  inheritNotYou,
	}
}

// TicketEvent is what happened on a ticket.
type TicketEvent int

const (
	// TicketCreated goes to administrators.
	TicketCreated TicketEvent = iota
	// TicketUserReplied goes to administrators.
	TicketUserReplied
	// TicketAdminReplied goes to the ticket creator.
	TicketAdminReplied
)

// Ticket is the content of a ticket notification.
type Ticket struct {
	Event    TicketEvent
	PublicID string
	Subject  string
	Status   string
	Preview  string
	URL      string
}

const (
	ticketSubjectMaxRunes = 60
	ticketPreviewMaxRunes = 1000
)

// TicketNotification announces new ticket activity. Ticket subjects and
// messages are user input; the layout escapes them.
func TicketNotification(t Ticket) Email {
	headline, title, intro := "工单通知", "工单有新动态", "工单有新的动态。"
	switch t.Event {
	case TicketCreated:
		headline, title, intro = "新工单", "收到新工单", "有用户提交了新工单。"
	case TicketUserReplied:
		headline, title, intro = "用户回复", "工单有用户回复", "用户在工单中发表了新回复。"
	case TicketAdminReplied:
		headline, title, intro = "工单有新回复", "您的工单有新回复", "管理员回复了您的工单。"
	}

	publicID := strings.TrimSpace(t.PublicID)
	ticketSubject := strings.TrimSpace(t.Subject)
	line := headline
	if ref := strings.TrimSpace(publicID + " " + truncateRunes(ticketSubject, ticketSubjectMaxRunes)); ref != "" {
		line += "：" + ref
	}

	e := Email{
		Subject: subject(line),
		Title:   title,
		Intro:   []string{intro},
		Details: nonEmptyDetails(
			Detail{Label: "工单编号", Value: publicID},
			Detail{Label: "主题", Value: ticketSubject},
			Detail{Label: "状态", Value: strings.TrimSpace(t.Status)},
		),
	}
	if preview := strings.TrimSpace(t.Preview); preview != "" {
		e.Quote = &Quote{Label: "内容摘要", Text: truncateRunes(preview, ticketPreviewMaxRunes)}
	}
	// A misconfigured frontend URL costs the button, not the notification.
	if url := strings.TrimSpace(t.URL); isWebURL(url) {
		e.Action = &Action{Label: "查看工单", URL: url}
	}
	return e
}

func truncateRunes(value string, maxRunes int) string {
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxRunes-1]) + "…"
}

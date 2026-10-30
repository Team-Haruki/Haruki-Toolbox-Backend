package mailtemplate

import (
	"strings"
	"testing"
	"time"
)

var sampleTime = time.Date(2026, time.October, 31, 6, 5, 9, 0, time.UTC)

type sampleEmail struct {
	name    string
	caption string
	email   Email
	// want lists values that must appear in both the HTML and the text part.
	want []string
}

// sampleEmails covers every email the backend sends, with realistic data.
func sampleEmails() []sampleEmail {
	activity := InheritVaultActivity{Server: "jp", MaskedGameUserID: "71****8204", IP: "203.0.113.9", At: sampleTime}
	return []sampleEmail{
		{"email-verification-code", "邮箱验证码（绑定 QQ 时发送）", EmailVerificationCode("483920", 5*time.Minute), []string{"483920", "5 分钟内有效"}},
		{"bot-verification-code", "HarukiBot NEO 注册 / 重置凭据验证码", BotVerificationCode("615204", 10*time.Minute), []string{"615204", "10 分钟内有效", "HarukiBot NEO"}},
		{"game-account-transferred", "游戏账号绑定已转移（通知原绑定者）", GameAccountTransferred("jp", "7105938261048204", sampleTime), []string{"JP", "7105938261048204", "2026-10-31 14:05:09（北京时间）"}},
		{"inherit-code-revealed", "引继保险库：引继码已被查看", InheritCodeRevealed(activity), []string{"查看引继码", "71****8204", "203.0.113.9", "2026-10-31 14:05:09（北京时间）", "修改 Haruki 工具箱密码"}},
		{"inherit-code-reissued", "引继保险库：引继码已重新签发", InheritCodeReissued(activity), []string{"重新签发引继码", "71****8204", "原引继码已失效"}},
		{"ticket-created", "工单：新工单（发给管理员）", TicketNotification(Ticket{
			Event: TicketCreated, PublicID: "TK-20261031-0042", Subject: "上传 MySekai 数据时提示超时",
			Status: "新建", Preview: "从昨天开始用 iOS 模块上传 MySekai 数据一直提示超时，Suite 数据正常。", URL: SiteURL + "/tickets/TK-20261031-0042",
		}), []string{"TK-20261031-0042", "新建", SiteURL + "/tickets/TK-20261031-0042"}},
		{"ticket-user-replied", "工单：用户回复（发给管理员）", TicketNotification(Ticket{
			Event: TicketUserReplied, PublicID: "TK-20261031-0042", Subject: "上传 MySekai 数据时提示超时",
			Status: "待管理员处理", Preview: "换成 Wi-Fi 之后还是一样。", URL: SiteURL + "/tickets/TK-20261031-0042",
		}), []string{"待管理员处理", "换成 Wi-Fi 之后还是一样。"}},
		{"ticket-admin-replied", "工单：管理员回复（发给提交者）", TicketNotification(Ticket{
			Event: TicketAdminReplied, PublicID: "TK-20261031-0042", Subject: "上传 MySekai 数据时提示超时",
			Status: "待用户回复", Preview: "已修复，请更新 iOS 模块后重试。", URL: SiteURL + "/tickets/TK-20261031-0042",
		}), []string{"待用户回复", "管理员回复了您的工单"}},
	}
}

func TestEveryEmailRendersHTMLAndText(t *testing.T) {
	for _, sample := range sampleEmails() {
		t.Run(sample.name, func(t *testing.T) {
			msg, err := Render(sample.email)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if !strings.HasSuffix(msg.Subject, " | "+Brand) {
				t.Fatalf("subject = %q", msg.Subject)
			}
			for part, body := range map[string]string{"html": msg.HTML, "text": msg.Text} {
				for _, want := range append([]string{Brand, sample.email.Title, SiteURL, footer}, sample.want...) {
					if !strings.Contains(body, want) {
						t.Errorf("%s part lacks %q", part, want)
					}
				}
				for _, bad := range []string{"<no value>", "{{", "%!"} {
					if strings.Contains(body, bad) {
						t.Errorf("%s part contains %q", part, bad)
					}
				}
			}
			for _, want := range []string{"<!DOCTYPE html>", `<meta name="color-scheme" content="light dark">`, "max-width:560px", "</html>"} {
				if !strings.Contains(msg.HTML, want) {
					t.Errorf("html lacks %q", want)
				}
			}
			if strings.Contains(msg.HTML, "gradient") || strings.Contains(msg.HTML, "<img") {
				t.Error("html must not use gradients or images")
			}
			if strings.Contains(msg.Text, "<") && !strings.Contains(sample.email.Title+strings.Join(sample.want, ""), "<") {
				t.Errorf("text part contains markup: %q", msg.Text)
			}
		})
	}
}

func TestActionURLAppearsAsButtonAndPlainText(t *testing.T) {
	msg, err := Render(TicketNotification(Ticket{Event: TicketCreated, PublicID: "TK-1", URL: "https://haruki.example/tickets/TK-1"}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(msg.HTML, `href="https://haruki.example/tickets/TK-1"`) != 2 || !strings.Contains(msg.HTML, ">https://haruki.example/tickets/TK-1</a>") {
		t.Fatalf("html must carry the URL as a button and as visible text: %s", msg.HTML)
	}
	if !strings.Contains(msg.Text, "查看工单：\nhttps://haruki.example/tickets/TK-1") {
		t.Fatalf("text = %q", msg.Text)
	}
}

func TestUserInputIsEscaped(t *testing.T) {
	msg, err := Render(TicketNotification(Ticket{
		Event: TicketAdminReplied, PublicID: "TK-1", Subject: `<img src=x onerror=alert(1)>`, Preview: `<script>alert(1)</script>`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg.HTML, "<script>") || strings.Contains(msg.HTML, "<img") {
		t.Fatalf("user input reached the html unescaped: %s", msg.HTML)
	}
	if !strings.Contains(msg.HTML, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatal("escaped preview missing")
	}
}

func TestTicketWithoutWebURLStillNotifies(t *testing.T) {
	for _, url := range []string{"haruki.example/tickets/TK-1", "ftp://haruki.example/tickets/TK-1", ""} {
		msg, err := Render(TicketNotification(Ticket{Event: TicketCreated, PublicID: "TK-1", Subject: "s", URL: url}))
		if err != nil {
			t.Fatalf("URL %q: %v", url, err)
		}
		if strings.Contains(msg.HTML, "查看工单") {
			t.Errorf("URL %q produced a button", url)
		}
	}
}

func TestActionURLIsTrimmed(t *testing.T) {
	action := &Action{Label: "查看", URL: " https://haruki.example/x\n"}
	msg, err := Render(Email{Subject: "s", Title: "t", Action: action})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.HTML, `href="https://haruki.example/x"`) || strings.Contains(msg.HTML, "%0a") {
		t.Fatalf("html = %s", msg.HTML)
	}
	if action.URL != " https://haruki.example/x\n" {
		t.Fatal("Render modified the caller's Action")
	}
}

func TestInheritNoticeSurvivesMissingFields(t *testing.T) {
	for _, compose := range []func(InheritVaultActivity) Email{InheritCodeRevealed, InheritCodeReissued} {
		msg, err := Render(compose(InheritVaultActivity{At: sampleTime}))
		if err != nil {
			t.Fatalf("security notice dropped: %v", err)
		}
		if strings.Contains(msg.Text, "IP 地址") || strings.Contains(msg.Text, "服务器") || !strings.Contains(msg.Text, "（北京时间）") {
			t.Fatalf("text = %q", msg.Text)
		}
	}
}

func TestQuoteKeepsLineBreaks(t *testing.T) {
	msg, err := Render(Email{Subject: "s", Title: "t", Quote: &Quote{Label: "l", Text: "1. a\n2. b"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.HTML, "white-space:pre-line;\">1. a\n2. b</td>") || !strings.Contains(msg.Text, "1. a\n2. b") {
		t.Fatalf("line breaks lost: %s", msg.HTML)
	}
}

func TestTicketPreviewIsBounded(t *testing.T) {
	e := TicketNotification(Ticket{Event: TicketCreated, PublicID: "TK-1", Preview: strings.Repeat("字", 5000)})
	if n := len([]rune(e.Quote.Text)); n != ticketPreviewMaxRunes {
		t.Fatalf("preview runes = %d", n)
	}
}

func TestRenderRejectsMissingValues(t *testing.T) {
	cases := map[string]Email{
		"empty code":       EmailVerificationCode(" ", 5*time.Minute),
		"no subject":       {Title: "t"},
		"no title":         {Subject: "s"},
		"empty detail":     GameAccountTransferred("", "1", sampleTime),
		"empty paragraph":  {Subject: "s", Title: "t", Intro: []string{""}},
		"action no url":    {Subject: "s", Title: "t", Action: &Action{Label: "go"}},
		"action bad url":   {Subject: "s", Title: "t", Action: &Action{Label: "go", URL: "javascript:alert(1)"}},
		"action relative":  {Subject: "s", Title: "t", Action: &Action{Label: "go", URL: "/tickets/1"}},
		"quote empty text": {Subject: "s", Title: "t", Quote: &Quote{Label: "l"}},
	}
	for name, email := range cases {
		if _, err := Render(email); err == nil {
			t.Errorf("%s: Render succeeded", name)
		}
	}
}

func TestOptionalBlocksAreOmitted(t *testing.T) {
	msg, err := Render(TicketNotification(Ticket{Event: TicketCreated, PublicID: "TK-1"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"内容摘要", "查看工单", "如果按钮无法打开"} {
		if strings.Contains(msg.HTML, absent) || strings.Contains(msg.Text, absent) {
			t.Errorf("unset block rendered %q", absent)
		}
	}
	if msg.Subject != "新工单：TK-1 | "+Brand {
		t.Fatalf("subject = %q", msg.Subject)
	}
}

func TestTicketSubjectIsTruncated(t *testing.T) {
	long := strings.Repeat("长", 80)
	e := TicketNotification(Ticket{Event: TicketCreated, PublicID: "TK-1", Subject: long})
	if want := "新工单：TK-1 " + strings.Repeat("长", 59) + "… | " + Brand; e.Subject != want {
		t.Fatalf("subject = %q", e.Subject)
	}
	if e.Details[1].Value != long {
		t.Fatal("details must keep the full ticket subject")
	}
}

func TestFormatTimeUsesCST(t *testing.T) {
	if got := FormatTime(time.Date(2026, 1, 1, 20, 30, 0, 0, time.UTC)); got != "2026-01-02 04:30:00（北京时间）" {
		t.Fatalf("FormatTime = %q", got)
	}
}

func TestCodeNeverOverstatesLifetime(t *testing.T) {
	if got := codeNote(90 * time.Second); got != "1 分钟内有效" {
		t.Fatalf("codeNote(90s) = %q", got)
	}
	if got := codeNote(10 * time.Minute); got != "10 分钟内有效" {
		t.Fatalf("codeNote(10m) = %q", got)
	}
	if got := codeNote(0); got != "1 分钟内有效" {
		t.Fatalf("codeNote(0) = %q", got)
	}
}

// Package mailtemplate renders every email the Toolbox sends through one
// shared layout: a text wordmark header, a content card and a short footer.
// An email is described as data (Email) and rendered twice from the same
// value, as HTML and as plain text, so the two parts never drift apart.
//
// The Kratos courier templates under external/kratos/courier-templates are
// generated from this layout as well (kratos.go), so self-service mail from
// Kratos looks the same as mail sent by the backend.
package mailtemplate

import (
	"bytes"
	_ "embed"
	"fmt"
	htmltemplate "html/template"
	"net/url"
	"strings"
	texttemplate "text/template"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/smtp"
)

const (
	// Brand is the wordmark shown at the top of every email and used in
	// subjects.
	Brand = "Haruki 工具箱"
	// SiteURL is the public site linked from every footer. It is the same
	// constant for backend and Kratos mail because Kratos templates cannot
	// read the backend configuration.
	SiteURL   = "https://haruki.seiunx.com"
	siteLabel = "haruki.seiunx.com"
	footer    = "此邮件由 Haruki 工具箱自动发送，请勿直接回复。如果并非您本人操作，请按上文提示处理，或通过工单联系我们。"
)

var (
	//go:embed layout.html.tmpl
	layoutHTMLSource string
	//go:embed layout.txt.tmpl
	layoutTextSource string

	layoutHTML = htmltemplate.Must(htmltemplate.New("layout.html").Option("missingkey=error").Parse(layoutHTMLSource))
	layoutText = texttemplate.Must(texttemplate.New("layout.txt").Option("missingkey=error").Parse(layoutTextSource))

	cst = time.FixedZone("UTC+8", 8*60*60)
)

// Email is the content of one email. Every block except Subject and Title is
// optional; the layout only draws the blocks that are set, in field order.
type Email struct {
	Subject string
	// Preheader is the one-line preview most inboxes show next to the
	// subject. It defaults to the first Intro paragraph.
	Preheader string
	Title     string
	Intro     []string
	Code      *Code
	Details   []Detail
	Quote     *Quote
	Action    *Action
	Outro     []string
	// Notice is the closing "if this wasn't you" line.
	Notice string
}

// Code is a one-time code shown in a monospace box.
type Code struct {
	Label string
	Value string
	Note  string
}

// Detail is one label/value row of the details table.
type Detail struct {
	Label string
	Value string
}

// Quote is user-provided text, such as a ticket message preview.
type Quote struct {
	Label string
	Text  string
}

// Action is the single primary button. The URL is repeated as plain text
// below the button for clients that block links.
type Action struct {
	Label string
	URL   string
}

type layoutData struct {
	Email
	Brand     string
	Footer    string
	SiteURL   string
	SiteLabel string
}

// Render renders e as a complete message with an HTML and a plain-text part.
func Render(e Email) (smtp.Message, error) {
	if err := e.validate(false); err != nil {
		return smtp.Message{}, err
	}
	return render(e)
}

func render(e Email) (smtp.Message, error) {
	if strings.TrimSpace(e.Preheader) == "" && len(e.Intro) > 0 {
		e.Preheader = e.Intro[0]
	}
	data := layoutData{Email: e, Brand: Brand, Footer: footer, SiteURL: SiteURL, SiteLabel: siteLabel}
	var html, text bytes.Buffer
	if err := layoutHTML.Execute(&html, data); err != nil {
		return smtp.Message{}, fmt.Errorf("render html mail: %w", err)
	}
	if err := layoutText.Execute(&text, data); err != nil {
		return smtp.Message{}, fmt.Errorf("render text mail: %w", err)
	}
	return smtp.Message{Subject: e.Subject, HTML: html.String(), Text: text.String()}, nil
}

// validate rejects an email with a missing required value, so a caller that
// forgets one fails loudly in tests instead of sending a hollow block.
// Placeholders (Kratos template variables) skip the URL scheme check.
func (e Email) validate(placeholders bool) error {
	var missing []string
	need := func(name, value string) {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	need("Subject", e.Subject)
	need("Title", e.Title)
	for i, p := range e.Intro {
		need(fmt.Sprintf("Intro[%d]", i), p)
	}
	if e.Code != nil {
		need("Code.Label", e.Code.Label)
		need("Code.Value", e.Code.Value)
	}
	for i, d := range e.Details {
		need(fmt.Sprintf("Details[%d].Label", i), d.Label)
		need(fmt.Sprintf("Details[%d].Value", i), d.Value)
	}
	if e.Quote != nil {
		need("Quote.Label", e.Quote.Label)
		need("Quote.Text", e.Quote.Text)
	}
	if e.Action != nil {
		need("Action.Label", e.Action.Label)
		need("Action.URL", e.Action.URL)
		if !placeholders && strings.TrimSpace(e.Action.URL) != "" && !isWebURL(e.Action.URL) {
			return fmt.Errorf("mail %q: Action.URL must be an absolute http(s) URL", e.Subject)
		}
	}
	for i, p := range e.Outro {
		need(fmt.Sprintf("Outro[%d]", i), p)
	}
	if len(missing) > 0 {
		return fmt.Errorf("mail %q: missing %s", e.Subject, strings.Join(missing, ", "))
	}
	return nil
}

// subject appends the brand to a subject line.
func subject(s string) string {
	return s + " | " + Brand
}

func isWebURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

// FormatTime formats t for display in mail, in China Standard Time.
func FormatTime(t time.Time) string {
	return t.In(cst).Format("2006-01-02 15:04:05") + "（北京时间）"
}

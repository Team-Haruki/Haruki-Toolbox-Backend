package smtp

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/mail"
	"strings"
	"time"
)

// Message is one rendered email: a subject plus the same content as HTML and
// as plain text. Both parts are always sent, as multipart/alternative.
type Message struct {
	Subject string
	HTML    string
	Text    string
}

func (m Message) validate() error {
	if strings.TrimSpace(m.Subject) == "" || strings.TrimSpace(m.HTML) == "" || strings.TrimSpace(m.Text) == "" {
		return errors.New("mail message needs a subject, an HTML part and a text part")
	}
	return nil
}

// validRecipients trims the recipient list and rejects it when it is empty or
// any entry is not a bare email address (which also rules out line breaks).
func validRecipients(to []string) ([]string, error) {
	out := make([]string, 0, len(to))
	for _, raw := range to {
		addr := strings.TrimSpace(raw)
		parsed, err := mail.ParseAddress(addr)
		if err != nil || parsed.Name != "" || parsed.Address != addr {
			return nil, fmt.Errorf("invalid mail recipient %q", addr)
		}
		out = append(out, addr)
	}
	if len(out) == 0 {
		return nil, errors.New("mail has no recipient")
	}
	return out, nil
}

// buildMIME serialises msg into an RFC 5322 message for already validated
// recipients. The subject is stripped of line breaks and RFC 2047 encoded, and both bodies are base64
// encoded so long HTML lines never exceed the SMTP line limit.
func buildMIME(from mail.Address, to []string, msg Message, now time.Time) ([]byte, error) {
	boundary, err := randomToken(16)
	if err != nil {
		return nil, err
	}
	messageID, err := randomToken(16)
	if err != nil {
		return nil, err
	}
	domain := "localhost"
	if at := strings.LastIndexByte(from.Address, '@'); at >= 0 && at < len(from.Address)-1 {
		domain = from.Address[at+1:]
	}

	var b strings.Builder
	writeHeader := func(name, value string) {
		b.WriteString(name)
		b.WriteString(": ")
		b.WriteString(value)
		b.WriteString("\r\n")
	}
	writeHeader("From", from.String())
	writeHeader("To", strings.Join(to, ", "))
	writeHeader("Subject", mime.BEncoding.Encode("UTF-8", singleLine(msg.Subject)))
	writeHeader("Date", now.Format(time.RFC1123Z))
	writeHeader("Message-ID", fmt.Sprintf("<%s@%s>", messageID, domain))
	writeHeader("MIME-Version", "1.0")
	writeHeader("Content-Type", fmt.Sprintf("multipart/alternative; boundary=%q", boundary))
	b.WriteString("\r\n")

	writePart := func(contentType, body string) {
		b.WriteString("--" + boundary + "\r\n")
		writeHeader("Content-Type", contentType+"; charset=UTF-8")
		writeHeader("Content-Transfer-Encoding", "base64")
		b.WriteString("\r\n")
		writeBase64Lines(&b, []byte(normalizeNewlines(body)))
	}
	// Plain text first: clients show the last part they can render.
	writePart("text/plain", msg.Text)
	writePart("text/html", msg.HTML)
	b.WriteString("--" + boundary + "--\r\n")
	return []byte(b.String()), nil
}

func writeBase64Lines(b *strings.Builder, data []byte) {
	encoded := base64.StdEncoding.EncodeToString(data)
	for len(encoded) > 76 {
		b.WriteString(encoded[:76])
		b.WriteString("\r\n")
		encoded = encoded[76:]
	}
	if encoded != "" {
		b.WriteString(encoded)
		b.WriteString("\r\n")
	}
}

func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

func singleLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '\r' || r == '\n' }), " ")
}

func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate message token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

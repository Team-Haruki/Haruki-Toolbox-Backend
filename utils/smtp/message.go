package smtp

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
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

// buildMIME serialises msg into an RFC 5322 message. Header values are
// stripped of line breaks and RFC 2047 encoded, and both bodies are base64
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
	writeHeader("To", strings.Join(sanitizeAddresses(to), ", "))
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

func sanitizeAddresses(to []string) []string {
	out := make([]string, 0, len(to))
	for _, addr := range to {
		if addr = strings.TrimSpace(singleLine(addr)); addr != "" {
			out = append(out, addr)
		}
	}
	return out
}

func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate message token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

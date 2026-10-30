package smtp

import (
	"bufio"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestBuildMIMESendsTextAndHTMLParts(t *testing.T) {
	msg := Message{
		Subject: "验证码 | Haruki 工具箱\r\nBcc: victim@example.com",
		HTML:    "<p>" + strings.Repeat("长", 400) + "</p>\n<p>second</p>",
		Text:    "第一行\n第二行",
	}
	raw, err := buildMIME(mail.Address{Name: "Haruki 工具箱", Address: "noreply@example.com"}, []string{"a@example.com", "b@example.com\r\nBcc: x@example.com"}, msg, time.Date(2026, 10, 31, 6, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 998 {
			t.Fatalf("line longer than the SMTP limit: %d", len(line))
		}
	}
	parsed, err := mail.ReadMessage(bufio.NewReader(strings.NewReader(string(raw))))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Header.Get("Bcc") != "" {
		t.Fatal("header injection through subject or recipient")
	}
	decoder := new(mime.WordDecoder)
	subject, err := decoder.DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil || subject != "验证码 | Haruki 工具箱 Bcc: victim@example.com" {
		t.Fatalf("subject = %q, %v", subject, err)
	}
	from, err := parsed.Header.AddressList("From")
	if err != nil || len(from) != 1 || from[0].Name != "Haruki 工具箱" || from[0].Address != "noreply@example.com" {
		t.Fatalf("from = %v, %v", from, err)
	}
	if id := parsed.Header.Get("Message-ID"); !strings.HasSuffix(id, "@example.com>") {
		t.Fatalf("Message-ID = %q", id)
	}
	mediaType, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/alternative" {
		t.Fatalf("content type = %q, %v", mediaType, err)
	}
	reader := multipart.NewReader(parsed.Body, params["boundary"])
	var types []string
	bodies := map[string]string{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if part.Header.Get("Content-Transfer-Encoding") != "base64" {
			t.Fatalf("part encoding = %q", part.Header.Get("Content-Transfer-Encoding"))
		}
		decoded, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, part))
		if err != nil {
			t.Fatal(err)
		}
		partType, partParams, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if partParams["charset"] != "UTF-8" {
			t.Fatalf("charset = %q", partParams["charset"])
		}
		types = append(types, partType)
		bodies[partType] = string(decoded)
	}
	if strings.Join(types, ",") != "text/plain,text/html" {
		t.Fatalf("parts = %v", types)
	}
	if bodies["text/plain"] != "第一行\r\n第二行" || !strings.Contains(bodies["text/html"], "<p>second</p>") {
		t.Fatalf("bodies = %q", bodies)
	}
}

func TestSenderNameFallsBackToConfigThenDefault(t *testing.T) {
	client := &HarukiSMTPClient{DisplayName: "Configured"}
	if got := client.senderName("Haruki Bot"); got != "Haruki Bot" {
		t.Fatalf("explicit name = %q", got)
	}
	if got := client.senderName(" "); got != "Configured" {
		t.Fatalf("configured name = %q", got)
	}
	if got := (&HarukiSMTPClient{}).senderName(""); got != DefaultDisplayName {
		t.Fatalf("default name = %q", got)
	}
}

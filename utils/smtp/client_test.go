package smtp

import (
	"errors"
	"fmt"
	"net"
	stdsmtp "net/smtp"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
)

func TestNormalizeSMTPTimeout(t *testing.T) {
	t.Parallel()

	if got := normalizeSMTPTimeout(0); got != defaultSMTPTimeout {
		t.Fatalf("normalizeSMTPTimeout(0) = %v, want %v", got, defaultSMTPTimeout)
	}
	custom := 3 * time.Second
	if got := normalizeSMTPTimeout(custom); got != custom {
		t.Fatalf("normalizeSMTPTimeout(custom) = %v, want %v", got, custom)
	}
}

func TestSendMailClassifiesDataAcceptance(t *testing.T) {
	for _, tc := range []struct {
		name, dataReply, quitReply string
		definiteRejection          bool
	}{
		{name: "data rejected", dataReply: "550 rejected", definiteRejection: true},
		{name: "reply lost after data"},
		{name: "quit failed after accepted data", dataReply: "250 queued", quitReply: "421 closing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clientConn, serverConn := net.Pipe()
			defer clientConn.Close()
			defer serverConn.Close()
			deadline := time.Now().Add(3 * time.Second)
			_ = clientConn.SetDeadline(deadline)
			_ = serverConn.SetDeadline(deadline)
			serverResult := make(chan error, 1)
			go func() {
				defer serverConn.Close()
				serverResult <- simulateSMTPAcceptance(textproto.NewConn(serverConn), tc.dataReply, tc.quitReply)
			}()

			client, err := stdsmtp.NewClient(clientConn, "localhost")
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			err = sendMailWithClient(client, stdsmtp.PlainAuth("", "fixture", "fixture", "localhost"),
				"sender@example.test", []string{"recipient@example.test"}, []byte("fixture message"))
			if err == nil || errors.Is(err, ErrMessageNotAccepted) != tc.definiteRejection {
				t.Fatalf("unexpected classification: %v", err)
			}
			if err := <-serverResult; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func simulateSMTPAcceptance(server *textproto.Conn, dataReply, quitReply string) error {
	if err := server.PrintfLine("220 fixture"); err != nil {
		return err
	}
	exchanges := []struct{ command, reply string }{
		{"EHLO ", "250 AUTH PLAIN"},
		{"AUTH PLAIN ", "235 authenticated"},
		{"MAIL FROM:", "250 sender accepted"},
		{"RCPT TO:", "250 recipient accepted"},
		{"DATA", "354 send data"},
	}
	for _, exchange := range exchanges {
		line, err := server.ReadLine()
		if err != nil || !strings.HasPrefix(line, exchange.command) {
			return fmt.Errorf("waiting for %s: unexpected command or read error %v", exchange.command, err)
		}
		if err := server.PrintfLine("%s", exchange.reply); err != nil {
			return err
		}
	}
	if _, err := server.ReadDotBytes(); err != nil || dataReply == "" {
		return err
	}
	if err := server.PrintfLine("%s", dataReply); err != nil || quitReply == "" {
		return err
	}
	line, err := server.ReadLine()
	if err != nil || line != "QUIT" {
		return fmt.Errorf("waiting for QUIT: unexpected command or read error %v", err)
	}
	return server.PrintfLine("%s", quitReply)
}

func TestNewSMTPClientTimeout(t *testing.T) {
	t.Parallel()

	client := NewSMTPClient(config.SMTPConfig{
		SMTPAddr: "smtp.example.com",
		SMTPPort: 465,
		SMTPMail: "noreply@example.com",
		SMTPPass: "secret",
	})
	if client.Timeout != defaultSMTPTimeout {
		t.Fatalf("NewSMTPClient default timeout = %v, want %v", client.Timeout, defaultSMTPTimeout)
	}

	client = NewSMTPClient(config.SMTPConfig{
		SMTPAddr:       "smtp.example.com",
		SMTPPort:       465,
		SMTPMail:       "noreply@example.com",
		SMTPPass:       "secret",
		MailName:       " Haruki 工具箱 ",
		TimeoutSeconds: 25,
	})
	if client.DisplayName != "Haruki 工具箱" {
		t.Fatalf("NewSMTPClient display name = %q", client.DisplayName)
	}
	if client.Timeout != 25*time.Second {
		t.Fatalf("NewSMTPClient configured timeout = %v, want %v", client.Timeout, 25*time.Second)
	}
}

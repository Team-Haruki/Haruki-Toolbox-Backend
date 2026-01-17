package smtp

import (
	"crypto/tls"
	"fmt"
	"haruki-suite/config"
	"net/smtp"
	"strings"
	"time"
)

func SendMailTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	host := strings.Split(addr, ":")[0]

	conn, err := tls.Dial("tcp", addr, &tls.Config{
		InsecureSkipVerify: false,
		ServerName:         host,
	})
	if err != nil {
		return fmt.Errorf("failed to dial TLS: %w", err)
	}

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("failed to create SMTP client: %w", err)
	}
	defer func(c *smtp.Client) {
		_ = c.Close()
	}(c)

	if err = c.Auth(auth); err != nil {
		return fmt.Errorf("failed to authenticate: %w", err)
	}

	if err = c.Mail(from); err != nil {
		return fmt.Errorf("failed to set mail from: %w", err)
	}

	for _, recipient := range to {
		if err = c.Rcpt(recipient); err != nil {
			return fmt.Errorf("failed to set recipient %s: %w", recipient, err)
		}
	}

	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("failed to get data writer: %w", err)
	}

	_, err = wc.Write(msg)
	if err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	err = wc.Close()
	if err != nil {
		return fmt.Errorf("failed to close data writer: %w", err)
	}

	if err = c.Quit(); err != nil {
		return fmt.Errorf("failed to quit SMTP client: %w", err)
	}

	return nil
}

type HarukiSMTPClient struct {
	Addr string
	Auth smtp.Auth
	From string
}

func NewSMTPClient(cfg config.SMTPConfig) *HarukiSMTPClient {
	addr := fmt.Sprintf("%s:%d", cfg.SMTPAddr, cfg.SMTPPort)
	auth := smtp.PlainAuth("", cfg.SMTPMail, cfg.SMTPPass, cfg.SMTPAddr)
	return &HarukiSMTPClient{
		Addr: addr,
		Auth: auth,
		From: cfg.SMTPMail,
	}
}

func (c *HarukiSMTPClient) Send(to []string, subject, body string, displayName string) error {
	headers := make(map[string]string)

	if displayName != "" {
		headers["From"] = fmt.Sprintf("%s <%s>", displayName, c.From)
	} else {
		headers["From"] = c.From
	}

	headers["To"] = strings.Join(to, ", ")
	headers["Subject"] = subject
	headers["MIME-Version"] = "1.0"
	headers["Content-Type"] = "text/html; charset=\"UTF-8\""
	headers["Date"] = time.Now().Format(time.RFC1123Z)

	var msgBuilder strings.Builder
	for k, v := range headers {
		_, err := fmt.Fprintf(&msgBuilder, "%s: %s\r\n", k, v)
		if err != nil {
			return err
		}
	}
	msgBuilder.WriteString("\r\n")
	msgBuilder.WriteString(body)

	return SendMailTLS(c.Addr, c.Auth, c.From, to, []byte(msgBuilder.String()))
}

package smtp

import (
	"crypto/tls"
	"fmt"
	"haruki-suite/config"
	"net"
	"net/smtp"
	"strings"
	"time"
)

const defaultSMTPTimeout = 10 * time.Second

func normalizeSMTPTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultSMTPTimeout
	}
	return timeout
}

func SendMailTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte, timeout time.Duration) error {
	timeout = normalizeSMTPTimeout(timeout)
	host := strings.Split(addr, ":")[0]
	client, err := dialSMTPImplicitTLS(addr, host, timeout)
	if err != nil {
		if shouldFallbackToStartTLS(err) {
			client, err = dialSMTPStartTLS(addr, host, timeout)
			if err != nil {
				return fmt.Errorf("failed to dial SMTP via implicit TLS and STARTTLS fallback: %w", err)
			}
		} else {
			return err
		}
	}
	defer func() {
		_ = client.Close()
	}()

	if err := sendMailWithClient(client, auth, from, to, msg); err != nil {
		return err
	}
	return nil
}

func sendMailWithClient(client *smtp.Client, auth smtp.Auth, from string, to []string, msg []byte) error {
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("failed to authenticate: %w", err)
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("failed to set mail from: %w", err)
	}
	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("failed to set recipient %s: %w", recipient, err)
		}
	}
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("failed to get data writer: %w", err)
	}
	if _, err := wc.Write(msg); err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("failed to close data writer: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("failed to quit SMTP client: %w", err)
	}
	return nil
}

func dialSMTPImplicitTLS(addr, host string, timeout time.Duration) (*smtp.Client, error) {
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
		InsecureSkipVerify: false,
		ServerName:         host,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to dial TLS: %w", err)
	}
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to set SMTP deadline: %w", err)
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to create SMTP client: %w", err)
	}
	return client, nil
}

func dialSMTPStartTLS(addr, host string, timeout time.Duration) (*smtp.Client, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("failed to dial SMTP: %w", err)
	}
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to set SMTP deadline: %w", err)
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to create SMTP client: %w", err)
	}
	if ok, _ := client.Extension("STARTTLS"); !ok {
		_ = client.Close()
		return nil, fmt.Errorf("SMTP server does not support STARTTLS")
	}
	if err := client.StartTLS(&tls.Config{
		InsecureSkipVerify: false,
		ServerName:         host,
	}); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("failed to start TLS: %w", err)
	}
	return client, nil
}

func shouldFallbackToStartTLS(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "first record does not look like a tls handshake")
}

type HarukiSMTPClient struct {
	Addr    string
	Auth    smtp.Auth
	From    string
	Timeout time.Duration
}

func NewSMTPClient(cfg config.SMTPConfig) *HarukiSMTPClient {
	addr := fmt.Sprintf("%s:%d", cfg.SMTPAddr, cfg.SMTPPort)
	auth := smtp.PlainAuth("", cfg.SMTPMail, cfg.SMTPPass, cfg.SMTPAddr)
	timeout := normalizeSMTPTimeout(time.Duration(cfg.TimeoutSeconds) * time.Second)
	return &HarukiSMTPClient{
		Addr:    addr,
		Auth:    auth,
		From:    cfg.SMTPMail,
		Timeout: timeout,
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
	return SendMailTLS(c.Addr, c.Auth, c.From, to, []byte(msgBuilder.String()), c.Timeout)
}

package smtp

import (
	"haruki-suite/config"
	"testing"
	"time"
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
		TimeoutSeconds: 25,
	})
	if client.Timeout != 25*time.Second {
		t.Fatalf("NewSMTPClient configured timeout = %v, want %v", client.Timeout, 25*time.Second)
	}
}

package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

func applySMTPConnectionURIFallback(cfg *Config) error {
	connectionURI := strings.TrimSpace(os.Getenv("SMTP_CONNECTION_URI"))
	if connectionURI == "" {
		return nil
	}
	parsed, err := url.Parse(connectionURI)
	if err != nil {
		return fmt.Errorf("parse SMTP_CONNECTION_URI: %w", err)
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return fmt.Errorf("parse SMTP_CONNECTION_URI: missing smtp host")
	}
	portValue := strings.TrimSpace(parsed.Port())
	if portValue == "" {
		switch strings.ToLower(strings.TrimSpace(parsed.Scheme)) {
		case "smtps":
			portValue = "465"
		case "smtp":
			portValue = "587"
		default:
			portValue = "25"
		}
	}
	port, err := strconv.Atoi(portValue)
	if err != nil {
		return fmt.Errorf("parse SMTP_CONNECTION_URI port %q: %w", portValue, err)
	}

	username := ""
	password := ""
	if parsed.User != nil {
		username = strings.TrimSpace(parsed.User.Username())
		if pass, ok := parsed.User.Password(); ok {
			password = strings.TrimSpace(pass)
		}
	}

	if strings.TrimSpace(cfg.UserSystem.SMTP.SMTPAddr) == "" {
		cfg.UserSystem.SMTP.SMTPAddr = host
	}
	if _, hasExplicitSMTPPortEnv := firstEnvValue("SMTP_PORT"); !hasExplicitSMTPPortEnv {
		cfg.UserSystem.SMTP.SMTPPort = port
	}
	if strings.TrimSpace(cfg.UserSystem.SMTP.SMTPMail) == "" {
		switch {
		case username != "":
			cfg.UserSystem.SMTP.SMTPMail = username
		default:
			fromAddress := strings.TrimSpace(os.Getenv("SMTP_FROM_ADDRESS"))
			if fromAddress != "" {
				cfg.UserSystem.SMTP.SMTPMail = fromAddress
			}
		}
	}
	if strings.TrimSpace(cfg.UserSystem.SMTP.SMTPPass) == "" && password != "" {
		cfg.UserSystem.SMTP.SMTPPass = password
	}
	return nil
}

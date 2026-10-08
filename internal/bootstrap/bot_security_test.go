package bootstrap

import (
	"strings"
	"testing"
)

func TestValidateBotSecurityConfig(t *testing.T) {
	cfg := validDeviceFlowTestConfig(t)
	for _, value := range []string{"", "  ", testInternalAPIHash, strings.ToUpper(testInternalAPIHash), " " + testInternalAPIHash + " "} {
		cfg.BotSecurity.IngestTokenSHA256 = value
		if err := validateBotSecurityConfig(cfg); err != nil {
			t.Fatalf("ingest_token_sha256 %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{
		testInternalAPIHash[:63],
		testInternalAPIHash + "0",
		"z" + testInternalAPIHash[1:],
		"plain-ingest-token-not-a-hash",
	} {
		cfg.BotSecurity.IngestTokenSHA256 = value
		err := validateBotSecurityConfig(cfg)
		if err == nil || !strings.Contains(err.Error(), "bot_security.ingest_token_sha256") {
			t.Fatalf("ingest_token_sha256 %q: err = %v, want an ingest_token_sha256 error", value, err)
		}
		if strings.Contains(err.Error(), value) {
			t.Fatalf("the error repeats the configured value: %v", err)
		}
	}
}

func TestBuildRefusesInvalidBotSecurityHashBeforeAcquiringResources(t *testing.T) {
	cfg := validDeviceFlowTestConfig(t)
	cfg.OAuth2.DeviceFlow.Enabled = false
	cfg.UserSystem.KratosPublicURL = "http://kratos:4433"
	cfg.UserSystem.KratosAdminURL = "http://kratos:4434"
	cfg.GameData.URL = "postgres://unused"
	cfg.BotSecurity.IngestTokenSHA256 = "not-hex"
	if _, err := Build(cfg); err == nil || !strings.Contains(err.Error(), "bot_security.ingest_token_sha256") {
		t.Fatalf("Build err = %v, want the bot security validation error", err)
	}
}

func TestNewBotSecurityIngestConfig(t *testing.T) {
	cfg := validDeviceFlowTestConfig(t)
	if newBotSecurityIngestConfig(cfg).Enabled() {
		t.Fatal("ingest enabled without ingest_token_sha256")
	}
	cfg.BotSecurity.IngestTokenSHA256 = testInternalAPIHash
	if !newBotSecurityIngestConfig(cfg).Enabled() {
		t.Fatal("ingest disabled with a valid ingest_token_sha256")
	}
}

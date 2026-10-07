package bootstrap

import (
	"strings"
	"testing"
)

const testInternalAPIHash = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

func TestValidateOAuth2InternalAPIConfig(t *testing.T) {
	cfg := validDeviceFlowTestConfig(t)
	for _, value := range []string{"", testInternalAPIHash, strings.ToUpper(testInternalAPIHash)} {
		cfg.OAuth2.InternalAPI.TokenSHA256 = value
		if err := validateOAuth2InternalAPIConfig(cfg); err != nil {
			t.Fatalf("token_sha256 %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{
		testInternalAPIHash[:63],
		testInternalAPIHash + "0",
		"z" + testInternalAPIHash[1:],
		"plain-internal-token-not-a-hash",
	} {
		cfg.OAuth2.InternalAPI.TokenSHA256 = value
		err := validateOAuth2InternalAPIConfig(cfg)
		if err == nil || !strings.Contains(err.Error(), "oauth2.internal_api.token_sha256") {
			t.Fatalf("token_sha256 %q: err = %v, want a token_sha256 error", value, err)
		}
		if strings.Contains(err.Error(), value) {
			t.Fatalf("the error repeats the configured value: %v", err)
		}
	}
}

func TestBuildRefusesInvalidInternalAPIHashBeforeAcquiringResources(t *testing.T) {
	cfg := validDeviceFlowTestConfig(t)
	cfg.OAuth2.DeviceFlow.Enabled = false
	cfg.UserSystem.KratosPublicURL = "http://kratos:4433"
	cfg.UserSystem.KratosAdminURL = "http://kratos:4434"
	cfg.GameData.URL = "postgres://unused"
	cfg.OAuth2.InternalAPI.TokenSHA256 = "not-hex"
	if _, err := Build(cfg); err == nil || !strings.Contains(err.Error(), "oauth2.internal_api.token_sha256") {
		t.Fatalf("Build err = %v, want the internal API validation error", err)
	}
}

func TestNewOAuth2InternalAPIConfig(t *testing.T) {
	cfg := validDeviceFlowTestConfig(t)
	if newOAuth2InternalAPIConfig(cfg).Enabled() {
		t.Fatal("internal API enabled without token_sha256")
	}
	cfg.OAuth2.InternalAPI.TokenSHA256 = testInternalAPIHash
	if !newOAuth2InternalAPIConfig(cfg).Enabled() {
		t.Fatal("internal API disabled with a valid token_sha256")
	}
}

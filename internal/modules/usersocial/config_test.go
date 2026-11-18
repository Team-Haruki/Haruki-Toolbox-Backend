package usersocial

import "testing"

func TestBotVerifyConfigInstancesAreIsolated(t *testing.T) {
	t.Parallel()

	first := NewBotVerifyConfig(BotVerifyConfigOptions{Token: "first-secret"})
	second := NewBotVerifyConfig(BotVerifyConfigOptions{Token: "second-secret"})

	if !first.authorizes("first-secret") {
		t.Fatal("first config should authorize its own token")
	}
	if first.authorizes("second-secret") {
		t.Fatal("first config should not authorize the second config token")
	}
	if !second.authorizes("second-secret") {
		t.Fatal("second config should authorize its own token")
	}
	if second.authorizes("first-secret") {
		t.Fatal("second config should not authorize the first config token")
	}
}

func TestBotVerifyConfigRejectsUnconfiguredAndExampleCredentials(t *testing.T) {
	t.Parallel()

	var botVerifyConfig BotVerifyConfig
	if botVerifyConfig.authorizes("configured-client-token") {
		t.Fatal("zero-value config must reject non-empty bearer tokens")
	}
	if botVerifyConfig.authorizes("") {
		t.Fatal("zero-value config must reject an empty token")
	}
	example := NewBotVerifyConfig(BotVerifyConfigOptions{Token: "change-me-social-platform-verify-token"})
	if example.authorizes("change-me-social-platform-verify-token") {
		t.Fatal("example configuration must not authorize bot verification")
	}
}

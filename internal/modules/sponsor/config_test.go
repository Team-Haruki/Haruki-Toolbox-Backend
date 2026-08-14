package sponsor

import (
	"testing"
	"time"
)

func TestAfdianConfigInstancesRemainIsolated(t *testing.T) {
	first := NewAfdianConfig(AfdianConfigOptions{
		UserID:         "first-user",
		APIToken:       "first-token",
		APIBaseURL:     " https://first.example.test/open/ ",
		RequestTimeout: 17 * time.Second,
		WebhookSecret:  " first-secret ",
		SyncEnabled:    true,
		SyncInterval:   7 * time.Minute,
	})
	second := NewAfdianConfig(AfdianConfigOptions{
		UserID:        "second-user",
		APIToken:      "second-token",
		APIBaseURL:    "https://second.example.test/open",
		WebhookSecret: "second-secret",
	})

	if got, want := first.baseURL(), "https://first.example.test/open"; got != want {
		t.Fatalf("first baseURL() = %q, want %q", got, want)
	}
	if got, want := second.baseURL(), "https://second.example.test/open"; got != want {
		t.Fatalf("second baseURL() = %q, want %q", got, want)
	}
	if got, want := first.WebhookSecret(), "first-secret"; got != want {
		t.Fatalf("first WebhookSecret() = %q, want %q", got, want)
	}
	if got, want := second.WebhookSecret(), "second-secret"; got != want {
		t.Fatalf("second WebhookSecret() = %q, want %q", got, want)
	}
	if !first.CredentialsConfigured() || !second.CredentialsConfigured() {
		t.Fatal("both independent configs should retain their credentials")
	}
	if !first.SyncEnabled() || second.SyncEnabled() {
		t.Fatalf("sync flags leaked between configs: first=%t second=%t", first.SyncEnabled(), second.SyncEnabled())
	}
	if got, want := first.timeout(), 17*time.Second; got != want {
		t.Fatalf("first timeout() = %s, want %s", got, want)
	}
	if got, want := first.SyncInterval(), 7*time.Minute; got != want {
		t.Fatalf("first SyncInterval() = %s, want %s", got, want)
	}
}

func TestAfdianConfigDefaultsPreserveExistingContracts(t *testing.T) {
	config := NewAfdianConfig(AfdianConfigOptions{
		RequestTimeout: time.Second,
	})

	if got := config.baseURL(); got != defaultAfdianAPIBaseURL {
		t.Fatalf("baseURL() = %q, want %q", got, defaultAfdianAPIBaseURL)
	}
	if got := config.timeout(); got != 10*time.Second {
		t.Fatalf("timeout() = %s, want 10s minimum", got)
	}
	if got := config.SyncInterval(); got != 5*time.Minute {
		t.Fatalf("SyncInterval() = %s, want historical 5m default", got)
	}
	if config.CredentialsConfigured() {
		t.Fatal("zero-value credentials must remain unconfigured")
	}
	if config.SyncEnabled() {
		t.Fatal("zero-value sync flag must remain disabled")
	}
}

func TestAfdianConfigRequiresBothCredentialsAfterTrimming(t *testing.T) {
	tests := []struct {
		name    string
		userID  string
		token   string
		wantSet bool
	}{
		{name: "both", userID: " user ", token: " token ", wantSet: true},
		{name: "missing user", token: "token"},
		{name: "missing token", userID: "user"},
		{name: "whitespace", userID: " ", token: "\t"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := NewAfdianConfig(AfdianConfigOptions{UserID: test.userID, APIToken: test.token})
			if got := config.CredentialsConfigured(); got != test.wantSet {
				t.Fatalf("CredentialsConfigured() = %t, want %t", got, test.wantSet)
			}
		})
	}
}

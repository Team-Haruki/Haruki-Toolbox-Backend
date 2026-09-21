package handler

import (
	"testing"
	"time"
)

func TestBirthdaySubscriptionConfigIsInstanceScoped(t *testing.T) {
	t.Parallel()

	first := NewBirthdaySubscriptionConfig(BirthdaySubscriptionConfigOptions{
		HMESInternalBaseURL: " https://first.example.test/ ",
		HMESInternalToken:   " first-token ",
		UserAgent:           " first-agent ",
		RequestTimeout:      3 * time.Second,
	})
	second := NewBirthdaySubscriptionConfig(BirthdaySubscriptionConfigOptions{
		HMESInternalBaseURL: "https://second.example.test",
		HMESInternalToken:   "second-token",
		UserAgent:           "second-agent",
		RequestTimeout:      7 * time.Second,
	})

	if first.hmesInternalBaseURL != " https://first.example.test/ " ||
		first.hmesInternalToken != " first-token " ||
		first.userAgent != " first-agent " ||
		first.timeout() != 3*time.Second {
		t.Fatalf("first config changed or normalized unexpectedly: %#v", first)
	}
	if second.hmesInternalBaseURL != "https://second.example.test" ||
		second.hmesInternalToken != "second-token" ||
		second.userAgent != "second-agent" ||
		second.timeout() != 7*time.Second {
		t.Fatalf("second config changed unexpectedly: %#v", second)
	}
}

func TestBirthdaySubscriptionConfigUsesHistoricalTimeoutDefault(t *testing.T) {
	t.Parallel()

	for _, configured := range []time.Duration{0, -time.Second} {
		cfg := NewBirthdaySubscriptionConfig(BirthdaySubscriptionConfigOptions{RequestTimeout: configured})
		if got := cfg.timeout(); got != 5*time.Second {
			t.Fatalf("timeout() = %s for configured %s, want 5s", got, configured)
		}
	}
}

package cloudflare

import (
	"haruki-suite/config"
	"testing"
)

func TestValidateTurnstileBypass(t *testing.T) {
	original := config.Cfg
	t.Cleanup(func() {
		config.Cfg = original
	})

	config.Cfg.UserSystem.TurnstileBypass = true

	resp, err := ValidateTurnstile("", "")
	if err != nil {
		t.Fatalf("ValidateTurnstile returned error: %v", err)
	}
	if resp == nil || !resp.Success {
		t.Fatalf("ValidateTurnstile bypass response = %#v, want success=true", resp)
	}
}

func TestTurnstileHTTPClientReuseByProxy(t *testing.T) {
	turnstileClientMu.Lock()
	originalClient := turnstileClient
	originalProxy := turnstileClientProxy
	turnstileClient = nil
	turnstileClientProxy = ""
	turnstileClientMu.Unlock()
	t.Cleanup(func() {
		turnstileClientMu.Lock()
		turnstileClient = originalClient
		turnstileClientProxy = originalProxy
		turnstileClientMu.Unlock()
	})

	clientA := turnstileHTTPClient("")
	clientB := turnstileHTTPClient("  ")
	if clientA != clientB {
		t.Fatalf("expected same client instance for same normalized proxy")
	}

	clientC := turnstileHTTPClient("http://127.0.0.1:8080")
	if clientC == clientA {
		t.Fatalf("expected a different client instance after proxy change")
	}

	clientD := turnstileHTTPClient("http://127.0.0.1:8080")
	if clientC != clientD {
		t.Fatalf("expected same client instance for unchanged proxy")
	}
}

package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func turnstileResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestClientBypass(t *testing.T) {
	t.Parallel()

	client := NewClient(Config{Bypass: true})
	client.httpClient.SetTransport(roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("bypass client made an HTTP request")
		return nil, nil
	}))

	resp, err := client.Verify(context.Background(), "", "")
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if resp == nil || !resp.Success {
		t.Fatalf("Verify bypass response = %#v, want success=true", resp)
	}
}

func TestNewClientConfiguresIndependentHTTPClients(t *testing.T) {
	t.Parallel()

	clientA := NewClient(Config{Proxy: "  http://127.0.0.1:8080  "})
	clientB := NewClient(Config{Timeout: 2 * time.Second})
	if clientA == clientB || clientA.httpClient == clientB.httpClient {
		t.Fatal("NewClient reused mutable client state")
	}
	if got := clientA.httpClient.GetClient().Timeout; got != defaultTimeout {
		t.Fatalf("default timeout = %s, want %s", got, defaultTimeout)
	}
	if !clientA.httpClient.IsProxySet() {
		t.Fatal("proxy was not configured")
	}
	if got := clientB.httpClient.GetClient().Timeout; got != 2*time.Second {
		t.Fatalf("configured timeout = %s, want 2s", got)
	}
	if clientB.httpClient.IsProxySet() {
		t.Fatal("unexpected proxy on independently configured client")
	}
}

func TestClientVerifySendsConfiguredPayload(t *testing.T) {
	t.Parallel()

	type contextKey string
	const requestMarker contextKey = "request-marker"
	client := NewClient(Config{Secret: "turnstile-secret"})
	client.httpClient.SetTransport(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != verifyURL {
			t.Errorf("request URL = %q, want %q", request.URL.String(), verifyURL)
		}
		if got := request.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		if got := request.Context().Value(requestMarker); got != "present" {
			t.Errorf("request context marker = %#v, want present", got)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		var payload map[string]string
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		want := map[string]string{
			"secret":   "turnstile-secret",
			"response": "challenge-response",
			"remoteip": "203.0.113.10",
		}
		for key, wantValue := range want {
			if payload[key] != wantValue {
				t.Errorf("payload[%q] = %q, want %q", key, payload[key], wantValue)
			}
		}
		return turnstileResponse(http.StatusOK, `{"success":true,"hostname":"example.com"}`), nil
	}))

	ctx := context.WithValue(context.Background(), requestMarker, "present")
	resp, err := client.Verify(ctx, "challenge-response", "203.0.113.10")
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if resp == nil || !resp.Success || resp.Hostname != "example.com" {
		t.Fatalf("Verify response = %#v", resp)
	}
}

func TestClientVerifyClassifiesUnavailableFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		transport   roundTripFunc
		wantMessage string
	}{
		{
			name: "request",
			transport: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("network down")
			},
			wantMessage: "request failed",
		},
		{
			name: "status",
			transport: func(*http.Request) (*http.Response, error) {
				return turnstileResponse(http.StatusServiceUnavailable, "unavailable"), nil
			},
			wantMessage: "unexpected status 503",
		},
		{
			name: "decode",
			transport: func(*http.Request) (*http.Response, error) {
				return turnstileResponse(http.StatusOK, "not-json"), nil
			},
			wantMessage: "decode failed",
		},
		{
			name: "service rejection",
			transport: func(*http.Request) (*http.Response, error) {
				return turnstileResponse(http.StatusOK, `{"success":false,"error-codes":["internal-error"]}`), nil
			},
			wantMessage: "turnstile service rejected request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := NewClient(Config{})
			client.httpClient.SetTransport(tt.transport)
			_, err := client.Verify(context.Background(), "response", "")
			if !IsTurnstileUnavailable(err) {
				t.Fatalf("Verify error = %v, want ErrTurnstileUnavailable", err)
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("Verify error = %q, want it to contain %q", err, tt.wantMessage)
			}
		})
	}
}

func TestClientVerifyReturnsClientRejection(t *testing.T) {
	t.Parallel()

	client := NewClient(Config{})
	client.httpClient.SetTransport(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return turnstileResponse(http.StatusOK, `{"success":false,"error-codes":["timeout-or-duplicate"]}`), nil
	}))

	resp, err := client.Verify(context.Background(), "response", "")
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if resp == nil || resp.Success {
		t.Fatalf("Verify response = %#v, want unsuccessful client rejection", resp)
	}
}

func TestNilClientIsUnavailable(t *testing.T) {
	t.Parallel()

	var client *Client
	_, err := client.Verify(context.Background(), "response", "")
	if !IsTurnstileUnavailable(err) {
		t.Fatalf("Verify error = %v, want ErrTurnstileUnavailable", err)
	}
	_, err = new(Client).Verify(context.Background(), "response", "")
	if !IsTurnstileUnavailable(err) {
		t.Fatalf("zero-value client Verify error = %v, want ErrTurnstileUnavailable", err)
	}
	_, err = Verify(context.Background(), nil, "response", "")
	if !IsTurnstileUnavailable(err) {
		t.Fatalf("package Verify error = %v, want ErrTurnstileUnavailable", err)
	}
}

func TestIsTurnstileServiceFailure(t *testing.T) {
	t.Parallel()

	if !isTurnstileServiceFailure([]string{"internal-error"}) {
		t.Fatal("expected internal-error to be treated as service failure")
	}
	if !isTurnstileServiceFailure([]string{"invalid-input-secret"}) {
		t.Fatal("expected invalid-input-secret to be treated as service failure")
	}
	if isTurnstileServiceFailure([]string{"timeout-or-duplicate"}) {
		t.Fatal("expected timeout-or-duplicate to be treated as client rejection")
	}
}

func TestIsTurnstileUnavailable(t *testing.T) {
	t.Parallel()

	if !IsTurnstileUnavailable(ErrTurnstileUnavailable) {
		t.Fatal("expected sentinel error to be detected")
	}
	if !IsTurnstileUnavailable(errors.Join(ErrTurnstileUnavailable, errors.New("upstream"))) {
		t.Fatal("expected wrapped sentinel error to be detected")
	}
	if IsTurnstileUnavailable(errors.New("other")) {
		t.Fatal("unexpected detection for unrelated error")
	}
}

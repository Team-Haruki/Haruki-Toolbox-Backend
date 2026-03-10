package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLoginWithKratosPasswordSuccess(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/self-service/login/api":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"flow-login-1"}`))
			return
		case r.Method == http.MethodPost && r.URL.Path == "/self-service/login":
			if flow := r.URL.Query().Get("flow"); flow != "flow-login-1" {
				t.Fatalf("flow query = %q, want %q", flow, "flow-login-1")
			}
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode payload failed: %v", err)
			}
			if payload["method"] != "password" {
				t.Fatalf("method = %v, want %q", payload["method"], "password")
			}
			if payload["identifier"] != "u@example.com" {
				t.Fatalf("identifier = %v, want %q", payload["identifier"], "u@example.com")
			}
			if payload["password"] != "secret" {
				t.Fatalf("password = %v, want %q", payload["password"], "secret")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"session_token":"session-login-1"}`))
			return
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", server.URL, "", "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	token, err := handler.LoginWithKratosPassword(context.Background(), "u@example.com", "secret")
	if err != nil {
		t.Fatalf("LoginWithKratosPassword returned error: %v", err)
	}
	if token != "session-login-1" {
		t.Fatalf("token = %q, want %q", token, "session-login-1")
	}
}

func TestRegisterWithKratosPasswordConflict(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/self-service/registration/api":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"flow-reg-1"}`))
			return
		case r.Method == http.MethodPost && r.URL.Path == "/self-service/registration":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"reason":"A user with this email address exists already."}}`))
			return
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", server.URL, "", "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	_, err := handler.RegisterWithKratosPassword(context.Background(), "u@example.com", "secret", map[string]any{"name": "u"})
	if err == nil {
		t.Fatalf("expected conflict error, got nil")
	}
	if !IsKratosIdentityConflictError(err) {
		t.Fatalf("expected conflict error, got %v", err)
	}
}

func TestLoginWithKratosPasswordInvalidCredentials(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/self-service/login/api":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"flow-login-2"}`))
			return
		case r.Method == http.MethodPost && r.URL.Path == "/self-service/login":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"reason":"The password is invalid"}}`))
			return
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", server.URL, "", "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	_, err := handler.LoginWithKratosPassword(context.Background(), "u@example.com", "wrong")
	if err == nil {
		t.Fatalf("expected invalid credentials error, got nil")
	}
	if !IsKratosInvalidCredentialsError(err) {
		t.Fatalf("expected invalid credentials error, got %v", err)
	}
}

func TestUsesKratosProvider(t *testing.T) {
	t.Parallel()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	if handler.UsesKratosProvider() {
		t.Fatalf("UsesKratosProvider should be false by default")
	}

	handler.ConfigureIdentityProvider("auto", "http://kratos.example", "", "", "", true, true, 2*time.Second, nil)
	if !handler.UsesKratosProvider() {
		t.Fatalf("UsesKratosProvider should be true when provider=auto and kratos URL set")
	}

	handler.ConfigureIdentityProvider("local", "http://kratos.example", "", "", "", true, true, 2*time.Second, nil)
	if handler.UsesKratosProvider() {
		t.Fatalf("UsesKratosProvider should be false when provider=local")
	}
}

func TestExtractKratosErrorReasonFallsBackToBody(t *testing.T) {
	t.Parallel()

	reason := extractKratosErrorReason(kratosErrorPayload{}, "raw-body-error")
	if reason != "raw-body-error" {
		t.Fatalf("reason = %q, want %q", reason, "raw-body-error")
	}

	reason = extractKratosErrorReason(kratosErrorPayload{}, "")
	if !strings.Contains(reason, "failed") {
		t.Fatalf("reason = %q, expected fallback message", reason)
	}
}

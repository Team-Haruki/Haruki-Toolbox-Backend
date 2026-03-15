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

func TestVerifyKratosPasswordRevokesTemporarySession(t *testing.T) {
	t.Parallel()

	var revokedSessionID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/self-service/login/api":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"verify-flow-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/self-service/login":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"session_token":"verify-session-token-1"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/sessions/whoami":
			if got := r.Header.Get("X-Session-Token"); got != "verify-session-token-1" {
				t.Fatalf("X-Session-Token = %q, want %q", got, "verify-session-token-1")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"kratos-session-id-1","active":true,"identity":{"id":"identity-verify-1","traits":{"email":"verify@example.com"}}}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/admin/sessions/kratos-session-id-1":
			revokedSessionID = "kratos-session-id-1"
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", server.URL, server.URL, "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	if err := handler.VerifyKratosPassword(context.Background(), "verify@example.com", "secret"); err != nil {
		t.Fatalf("VerifyKratosPassword returned error: %v", err)
	}
	if revokedSessionID != "kratos-session-id-1" {
		t.Fatalf("revokedSessionID = %q, want %q", revokedSessionID, "kratos-session-id-1")
	}
}

func TestVerifyKratosPasswordReturnsErrorWhenRevokeFails(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/self-service/login/api":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"verify-flow-2"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/self-service/login":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"session_token":"verify-session-token-2"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/sessions/whoami":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"kratos-session-id-2","active":true,"identity":{"id":"identity-verify-2","traits":{"email":"verify2@example.com"}}}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/admin/sessions/kratos-session-id-2":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"temporary unavailable"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", server.URL, server.URL, "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	err := handler.VerifyKratosPassword(context.Background(), "verify2@example.com", "secret")
	if err == nil {
		t.Fatalf("expected revoke failure error, got nil")
	}
	if !IsIdentityProviderUnavailableError(err) {
		t.Fatalf("expected identity provider unavailable error, got %v", err)
	}
}

func TestVerifyKratosPasswordByIdentityIDSuccess(t *testing.T) {
	t.Parallel()

	var revokedSessionID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/identities/identity-verify-id-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"identity-verify-id-1","traits":{"email":" verify-id@example.com "}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/self-service/login/api":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"verify-by-id-flow-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/self-service/login":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode payload failed: %v", err)
			}
			if payload["identifier"] != "verify-id@example.com" {
				t.Fatalf("identifier = %v, want %q", payload["identifier"], "verify-id@example.com")
			}
			if payload["password"] != "secret" {
				t.Fatalf("password = %v, want %q", payload["password"], "secret")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"session_token":"verify-by-id-token-1"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/sessions/whoami":
			if got := r.Header.Get("X-Session-Token"); got != "verify-by-id-token-1" {
				t.Fatalf("X-Session-Token = %q, want %q", got, "verify-by-id-token-1")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"kratos-session-id-by-id-1","active":true,"identity":{"id":"identity-verify-id-1","traits":{"email":"verify-id@example.com"}}}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/admin/sessions/kratos-session-id-by-id-1":
			revokedSessionID = "kratos-session-id-by-id-1"
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", server.URL, server.URL, "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	if err := handler.VerifyKratosPasswordByIdentityID(context.Background(), "identity-verify-id-1", "secret"); err != nil {
		t.Fatalf("VerifyKratosPasswordByIdentityID returned error: %v", err)
	}
	if revokedSessionID != "kratos-session-id-by-id-1" {
		t.Fatalf("revokedSessionID = %q, want %q", revokedSessionID, "kratos-session-id-by-id-1")
	}
}

func TestVerifyKratosPasswordByIdentityIDIdentityNotFound(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/admin/identities/missing-identity" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", server.URL, server.URL, "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	err := handler.VerifyKratosPasswordByIdentityID(context.Background(), "missing-identity", "secret")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !IsKratosIdentityUnmappedError(err) {
		t.Fatalf("expected kratos identity unmapped error, got %v", err)
	}
}

func TestUsesKratosProvider(t *testing.T) {
	t.Parallel()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "")
	if handler.UsesKratosProvider() {
		t.Fatalf("UsesKratosProvider should be false by default")
	}

	handler.ConfigureIdentityProvider("kratos", "http://kratos.example", "", "", "", true, true, 2*time.Second, nil)
	if !handler.UsesKratosProvider() {
		t.Fatalf("UsesKratosProvider should be true when provider=kratos and kratos URL set")
	}

	handler.ConfigureIdentityProvider("local", "http://kratos.example", "", "", "", true, true, 2*time.Second, nil)
	if !handler.UsesKratosProvider() {
		t.Fatalf("legacy provider aliases should be coerced to kratos mode")
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

func TestUpdateKratosPasswordByIdentityIDPatchSuccess(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/admin/identities/identity-1" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/admin/identities/identity-1")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", "http://unused", server.URL, "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	if err := handler.UpdateKratosPasswordByIdentityID(context.Background(), "identity-1", "new-pass"); err != nil {
		t.Fatalf("UpdateKratosPasswordByIdentityID returned error: %v", err)
	}
}

func TestUpdateKratosPasswordByIdentityIDFallbackToPut(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			w.WriteHeader(http.StatusMethodNotAllowed)
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"identity-2","schema_id":"default","state":"active","traits":{"email":"u@example.com"}}`))
		case http.MethodPut:
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode payload failed: %v", err)
			}
			credentials, ok := payload["credentials"].(map[string]any)
			if !ok {
				t.Fatalf("credentials missing in update payload")
			}
			password, ok := credentials["password"].(map[string]any)
			if !ok {
				t.Fatalf("password credentials missing in update payload")
			}
			configValue, ok := password["config"].(map[string]any)
			if !ok {
				t.Fatalf("password config missing in update payload")
			}
			if configValue["password"] != "new-pass-2" {
				t.Fatalf("password value = %v, want %q", configValue["password"], "new-pass-2")
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", "http://unused", server.URL, "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	if err := handler.UpdateKratosPasswordByIdentityID(context.Background(), "identity-2", "new-pass-2"); err != nil {
		t.Fatalf("UpdateKratosPasswordByIdentityID returned error: %v", err)
	}
}

func TestStartKratosRecoveryByEmailCodeSuccess(t *testing.T) {
	t.Parallel()

	var recoveryMethods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/self-service/recovery/api":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"flow-recovery-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/self-service/recovery":
			if flow := r.URL.Query().Get("flow"); flow != "flow-recovery-1" {
				t.Fatalf("flow query = %q, want %q", flow, "flow-recovery-1")
			}
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode payload failed: %v", err)
			}
			method, _ := payload["method"].(string)
			email, _ := payload["email"].(string)
			recoveryMethods = append(recoveryMethods, method)
			if method != "code" {
				t.Fatalf("method = %q, want %q", method, "code")
			}
			if email != "recover@example.com" {
				t.Fatalf("email = %q, want %q", email, "recover@example.com")
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"state":"sent_email"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", server.URL, "", "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	if err := handler.StartKratosRecoveryByEmail(context.Background(), "recover@example.com"); err != nil {
		t.Fatalf("StartKratosRecoveryByEmail returned error: %v", err)
	}
	if len(recoveryMethods) != 1 || recoveryMethods[0] != "code" {
		t.Fatalf("recoveryMethods = %v, want [code]", recoveryMethods)
	}
}

func TestStartKratosRecoveryByEmailCodeStrategyDisabled(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/self-service/recovery/api":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"flow-recovery-code-only"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/self-service/recovery":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"reason":"strategy not enabled"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", server.URL, "", "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	err := handler.StartKratosRecoveryByEmail(context.Background(), "recover@example.com")
	if err == nil {
		t.Fatalf("expected strategy-disabled error, got nil")
	}
	if !IsKratosInvalidCredentialsError(err) && !IsKratosInvalidInputError(err) {
		t.Fatalf("expected invalid credentials or invalid input error, got %v", err)
	}
}

func TestResetKratosPasswordByRecoveryCodeSuccess(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/self-service/recovery/api":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"flow-recovery-reset-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/self-service/recovery":
			if flow := r.URL.Query().Get("flow"); flow != "flow-recovery-reset-1" {
				t.Fatalf("flow query = %q, want %q", flow, "flow-recovery-reset-1")
			}
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode payload failed: %v", err)
			}
			if payload["method"] != "code" {
				t.Fatalf("method = %v, want %q", payload["method"], "code")
			}
			if payload["code"] != "recovery-code-1" {
				t.Fatalf("code = %v, want %q", payload["code"], "recovery-code-1")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"state":"passed_challenge","continue_with":[{"action":"set_ory_session_token","ory_session_token":"session-from-recovery-1"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/sessions/whoami":
			if got := r.Header.Get("X-Session-Token"); got != "session-from-recovery-1" {
				t.Fatalf("X-Session-Token = %q, want %q", got, "session-from-recovery-1")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"active":true,"identity":{"id":"identity-reset-1","traits":{"email":"recover@example.com"}}}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/admin/identities/identity-reset-1":
			var patchPayload []map[string]any
			if err := json.NewDecoder(r.Body).Decode(&patchPayload); err != nil {
				t.Fatalf("Decode patch payload failed: %v", err)
			}
			if len(patchPayload) != 1 {
				t.Fatalf("patch payload length = %d, want 1", len(patchPayload))
			}
			if patchPayload[0]["path"] != "/credentials/password/config/password" {
				t.Fatalf("patch path = %v", patchPayload[0]["path"])
			}
			if patchPayload[0]["value"] != "new-password-1" {
				t.Fatalf("patch value = %v, want %q", patchPayload[0]["value"], "new-password-1")
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", server.URL, server.URL, "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)
	handler.KratosIdentityResolver = func(ctx context.Context, identityID string, email string) (string, error) {
		if identityID != "identity-reset-1" {
			t.Fatalf("identityID = %q, want %q", identityID, "identity-reset-1")
		}
		if email != "recover@example.com" {
			t.Fatalf("email = %q, want %q", email, "recover@example.com")
		}
		return "1000000001", nil
	}

	userID, identityID, err := handler.ResetKratosPasswordByRecoveryCode(context.Background(), "recovery-code-1", "new-password-1")
	if err != nil {
		t.Fatalf("ResetKratosPasswordByRecoveryCode returned error: %v", err)
	}
	if userID != "1000000001" {
		t.Fatalf("userID = %q, want %q", userID, "1000000001")
	}
	if identityID != "identity-reset-1" {
		t.Fatalf("identityID = %q, want %q", identityID, "identity-reset-1")
	}
}

func TestResetKratosPasswordByRecoveryCodeInvalidCode(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/self-service/recovery/api":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"flow-recovery-reset-2"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/self-service/recovery":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"reason":"The recovery code is invalid or has expired."}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", server.URL, server.URL, "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	_, _, err := handler.ResetKratosPasswordByRecoveryCode(context.Background(), "bad-code", "new-password-2")
	if err == nil {
		t.Fatalf("expected invalid code error, got nil")
	}
	if !IsKratosInvalidCredentialsError(err) {
		t.Fatalf("expected invalid credentials error, got %v", err)
	}
}

func TestResetKratosPasswordByRecoveryCodeParsesTopLevelSessionToken(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/self-service/recovery/api":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"flow-recovery-reset-top-level"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/self-service/recovery":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"state":"passed_challenge","session_token":"session-from-top-level"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/sessions/whoami":
			if got := r.Header.Get("X-Session-Token"); got != "session-from-top-level" {
				t.Fatalf("X-Session-Token = %q, want %q", got, "session-from-top-level")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"active":true,"identity":{"id":"identity-reset-2","traits":{"email":"recover2@example.com"}}}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/admin/identities/identity-reset-2":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", server.URL, server.URL, "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)
	handler.KratosIdentityResolver = func(ctx context.Context, identityID string, email string) (string, error) {
		if identityID != "identity-reset-2" {
			t.Fatalf("identityID = %q, want %q", identityID, "identity-reset-2")
		}
		if email != "recover2@example.com" {
			t.Fatalf("email = %q, want %q", email, "recover2@example.com")
		}
		return "1000000002", nil
	}

	userID, identityID, err := handler.ResetKratosPasswordByRecoveryCode(context.Background(), "recovery-code-top", "new-password-2")
	if err != nil {
		t.Fatalf("ResetKratosPasswordByRecoveryCode returned error: %v", err)
	}
	if userID != "1000000002" {
		t.Fatalf("userID = %q, want %q", userID, "1000000002")
	}
	if identityID != "identity-reset-2" {
		t.Fatalf("identityID = %q, want %q", identityID, "identity-reset-2")
	}
}

func TestRevokeKratosSessionsByIdentityID(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/admin/identities/identity-revoke-1/sessions" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/admin/identities/identity-revoke-1/sessions")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", "http://unused", server.URL, "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	if err := handler.RevokeKratosSessionsByIdentityID(context.Background(), "identity-revoke-1"); err != nil {
		t.Fatalf("RevokeKratosSessionsByIdentityID returned error: %v", err)
	}
}

func TestRevokeKratosSessionsByIdentityIDNotFound(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"reason":"identity not found"}}`))
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", "http://unused", server.URL, "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	err := handler.RevokeKratosSessionsByIdentityID(context.Background(), "identity-revoke-not-found")
	if err == nil {
		t.Fatalf("expected not found error, got nil")
	}
	if !IsKratosIdentityUnmappedError(err) {
		t.Fatalf("expected unmapped error, got %v", err)
	}
}

func TestListKratosSessionsByIdentityID(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/admin/identities/identity-list-1/sessions" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/admin/identities/identity-list-1/sessions")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"session-a","active":true,"expires_at":"2030-01-02T03:04:05Z"},{"id":"session-b","active":false}]`))
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", "http://unused", server.URL, "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	sessions, err := handler.ListKratosSessionsByIdentityID(context.Background(), "identity-list-1")
	if err != nil {
		t.Fatalf("ListKratosSessionsByIdentityID returned error: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("len(sessions) = %d, want 2", len(sessions))
	}
	if sessions[0].ID != "session-a" || !sessions[0].Active || sessions[0].ExpiresAt == nil {
		t.Fatalf("unexpected first session: %#v", sessions[0])
	}
	if sessions[1].ID != "session-b" || sessions[1].Active || sessions[1].ExpiresAt != nil {
		t.Fatalf("unexpected second session: %#v", sessions[1])
	}
}

func TestResolveKratosSessionID(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/sessions/whoami" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("X-Session-Token"); got != "resolve-session-token" {
			t.Fatalf("X-Session-Token = %q, want %q", got, "resolve-session-token")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"session-resolve-1","active":true,"identity":{"id":"identity-resolve-1","traits":{"email":"resolve-session@example.com"}}}`))
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", server.URL, "http://unused", "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	sessionID, err := handler.ResolveKratosSessionID(context.Background(), "resolve-session-token", "")
	if err != nil {
		t.Fatalf("ResolveKratosSessionID returned error: %v", err)
	}
	if sessionID != "session-resolve-1" {
		t.Fatalf("sessionID = %q, want %q", sessionID, "session-resolve-1")
	}
}

func TestFindKratosIdentityIDByEmail(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/admin/identities" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("credentials_identifier"); got != "resolve@example.com" {
			t.Fatalf("credentials_identifier = %q, want %q", got, "resolve@example.com")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"identity-by-email-1","traits":{"email":"resolve@example.com"}}]`))
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", "http://unused", server.URL, "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	identityID, err := handler.FindKratosIdentityIDByEmail(context.Background(), "resolve@example.com")
	if err != nil {
		t.Fatalf("FindKratosIdentityIDByEmail returned error: %v", err)
	}
	if identityID != "identity-by-email-1" {
		t.Fatalf("identityID = %q, want %q", identityID, "identity-by-email-1")
	}
}

func TestFindKratosIdentityIDByEmailNotFound(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/admin/identities" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", "http://unused", server.URL, "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	_, err := handler.FindKratosIdentityIDByEmail(context.Background(), "notfound@example.com")
	if err == nil {
		t.Fatalf("expected not found error, got nil")
	}
	if !IsKratosIdentityUnmappedError(err) {
		t.Fatalf("expected unmapped error, got %v", err)
	}
}

func TestUpdateKratosEmailByIdentityIDFallbackToPut(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			w.WriteHeader(http.StatusMethodNotAllowed)
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"identity-email-1","schema_id":"default","state":"active","traits":{"email":"old@example.com"}}`))
		case http.MethodPut:
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode payload failed: %v", err)
			}
			traits, ok := payload["traits"].(map[string]any)
			if !ok {
				t.Fatalf("traits missing in update payload")
			}
			if traits["email"] != "new@example.com" {
				t.Fatalf("traits.email = %v, want %q", traits["email"], "new@example.com")
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", "http://unused", server.URL, "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	if err := handler.UpdateKratosEmailByIdentityID(context.Background(), "identity-email-1", "new@example.com"); err != nil {
		t.Fatalf("UpdateKratosEmailByIdentityID returned error: %v", err)
	}
}

func TestUpdateKratosEmailByIdentityIDConflict(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"reason":"A user with this email already exists."}}`))
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", "http://unused", server.URL, "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	err := handler.UpdateKratosEmailByIdentityID(context.Background(), "identity-email-2", "dup@example.com")
	if err == nil {
		t.Fatalf("expected conflict error, got nil")
	}
	if !IsKratosIdentityConflictError(err) {
		t.Fatalf("expected conflict error, got %v", err)
	}
}

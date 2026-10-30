package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestKratosIdentityHasPassword(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/admin/identities/with-password":
			_, _ = w.Write([]byte(`{"id":"with-password","traits":{"email":"a@example.com"},"credentials":{"password":{"type":"password","identifiers":["a@example.com"]},"oidc":{"type":"oidc"}}}`))
		case "/admin/identities/social-only":
			_, _ = w.Write([]byte(`{"id":"social-only","traits":{"email":"b@example.com"},"credentials":{"oidc":{"type":"oidc","identifiers":["github:1"]}}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404}}`))
		}
	}))
	defer server.Close()

	handler := NewSessionHandler(newSessionTestRedisClient(t), "local-sign-key")
	handler.ConfigureIdentityProvider("kratos", "http://unused", server.URL, "X-Session-Token", "ory_kratos_session", true, true, 2*time.Second, nil)

	for id, want := range map[string]bool{"with-password": true, "social-only": false} {
		got, err := handler.KratosIdentityHasPassword(context.Background(), id)
		if err != nil || got != want {
			t.Fatalf("KratosIdentityHasPassword(%s) = %v, %v; want %v", id, got, err, want)
		}
	}
	if got, err := handler.KratosIdentityHasPassword(context.Background(), "missing"); err == nil || got {
		t.Fatalf("missing identity = %v, %v; want an error", got, err)
	}
	if got, err := handler.KratosIdentityHasPassword(context.Background(), " "); err == nil || got {
		t.Fatalf("empty identity = %v, %v; want an error", got, err)
	}
}

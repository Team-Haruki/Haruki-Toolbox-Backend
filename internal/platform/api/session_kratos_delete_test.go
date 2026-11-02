package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeleteKratosIdentityDoesNotHideTransportFailures(t *testing.T) {
	h := NewSessionHandler(nil, "")
	h.KratosAdminURL = "://invalid"
	if h.DeleteKratosIdentity(t.Context(), "identity-1") == nil {
		t.Fatal("invalid provider URL succeeded")
	}
	h.KratosAdminURL = "http://127.0.0.1:1"
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if h.DeleteKratosIdentity(ctx, "identity-1") == nil {
		t.Fatal("cancelled provider request succeeded")
	}
}

func TestDeleteKratosIdentityResponse(t *testing.T) {
	for _, tc := range []struct {
		status  int
		success bool
	}{{200, true}, {204, true}, {404, true}, {202, false}, {403, false}, {503, false}} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete || r.URL.Path != "/admin/identities/identity-1" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"secret":"must-not-appear-in-errors"}`))
			}))
			t.Cleanup(server.Close)
			handler := NewSessionHandler(nil, "")
			handler.KratosAdminURL = server.URL
			err := handler.DeleteKratosIdentity(t.Context(), "identity-1")
			if (err == nil) != tc.success {
				t.Fatalf("status=%d error=%v", tc.status, err)
			}
			if err != nil && strings.Contains(err.Error(), "must-not-appear") {
				t.Fatal("provider body leaked")
			}
		})
	}
	var missing *SessionHandler
	if missing.DeleteKratosIdentity(t.Context(), "identity-1") == nil {
		t.Fatal("missing provider succeeded")
	}
	if NewSessionHandler(nil, "").DeleteKratosIdentity(t.Context(), "") == nil {
		t.Fatal("empty identity succeeded")
	}
}

func TestDeletedKratosIdentityCannotProvisionFromStaleProxyHeaders(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		email   string
		success bool
	}{
		{"deleted", 404, "owner@example.test", false},
		{"provider-unavailable", 503, "owner@example.test", false},
		{"changed-email", 200, "different@example.test", false},
		{"live-identity", 200, "owner@example.test", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newSessionHandlerTestDB(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"id":"identity-1","traits":{"email":"` + tc.email + `"}}`))
			}))
			t.Cleanup(server.Close)
			h := NewSessionHandler(nil, "")
			h.DBClient, h.KratosAdminURL, h.KratosAutoProvisionUser = db, server.URL, true
			id, err := h.resolveKratosIdentity(t.Context(), "identity-1", "owner@example.test", true)
			if (err == nil) != tc.success {
				t.Fatalf("id=%q error=%v", id, err)
			}
			if !tc.success && db.User.Query().CountX(t.Context()) != 0 {
				t.Fatal("stale identity recreated account")
			}
		})
	}
}

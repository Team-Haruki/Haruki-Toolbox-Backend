package oauth2

import (
	"context"
	"haruki-suite/config"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHydraOAuthManagementEnabledFollowsProvider(t *testing.T) {
	original := config.Cfg
	t.Cleanup(func() {
		config.Cfg = original
	})

	config.Cfg.OAuth2.Provider = "hydra"
	config.Cfg.OAuth2.HydraAdminURL = ""
	if !HydraOAuthManagementEnabled() {
		t.Fatalf("expected hydra provider to enable hydra management mode even without admin url")
	}

	config.Cfg.OAuth2.Provider = "builtin"
	if HydraOAuthManagementEnabled() {
		t.Fatalf("expected builtin provider to disable hydra management mode")
	}
}

func TestListHydraConsentSessionsPaginates(t *testing.T) {
	original := config.Cfg
	t.Cleanup(func() {
		config.Cfg = original
	})

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/oauth2/auth/sessions/consent" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if got := r.URL.Query().Get("subject"); got != "u-1" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"unexpected subject"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page_token") {
		case "":
			w.Header().Set("Link", `<`+server.URL+`/admin/oauth2/auth/sessions/consent?subject=u-1&page_size=500&page_token=page-2>; rel="next"`)
			_, _ = w.Write([]byte(`[{"consent_request_id":"c1","grant_scope":["user:read"],"consent_request":{"client":{"client_id":"client-1","client_name":"Client 1","token_endpoint_auth_method":"none"}}}]`))
		case "page-2":
			_, _ = w.Write([]byte(`[{"consent_request_id":"c2","grant_scope":["bindings:read"],"consent_request":{"client":{"client_id":"client-2","client_name":"Client 2","token_endpoint_auth_method":"client_secret_basic"}}}]`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	config.Cfg.OAuth2.HydraAdminURL = server.URL
	config.Cfg.OAuth2.HydraRequestTimeoutSecond = 5

	sessions, err := ListHydraConsentSessions(context.Background(), "u-1")
	if err != nil {
		t.Fatalf("ListHydraConsentSessions returned error: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("len(sessions) = %d, want 2", len(sessions))
	}
	if sessions[0].ConsentRequest.Client.ClientID != "client-1" {
		t.Fatalf("first client id = %q, want %q", sessions[0].ConsentRequest.Client.ClientID, "client-1")
	}
	if sessions[1].ConsentRequest.Client.ClientID != "client-2" {
		t.Fatalf("second client id = %q, want %q", sessions[1].ConsentRequest.Client.ClientID, "client-2")
	}
}

func TestHydraConsentSessionExistsForClient(t *testing.T) {
	original := config.Cfg
	t.Cleanup(func() {
		config.Cfg = original
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/oauth2/auth/sessions/consent" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"consent_request_id":"c1","grant_scope":["user:read"],"consent_request":{"client":{"client_id":"client-a","client_name":"Client A","token_endpoint_auth_method":"none"}}}]`))
	}))
	defer server.Close()

	config.Cfg.OAuth2.HydraAdminURL = server.URL
	config.Cfg.OAuth2.HydraRequestTimeoutSecond = 5

	exists, err := HydraConsentSessionExistsForClient(context.Background(), "u-1", "client-a")
	if err != nil {
		t.Fatalf("HydraConsentSessionExistsForClient returned error: %v", err)
	}
	if !exists {
		t.Fatalf("expected client-a to exist")
	}

	exists, err = HydraConsentSessionExistsForClient(context.Background(), "u-1", "client-b")
	if err != nil {
		t.Fatalf("HydraConsentSessionExistsForClient returned error: %v", err)
	}
	if exists {
		t.Fatalf("expected client-b to be absent")
	}
}

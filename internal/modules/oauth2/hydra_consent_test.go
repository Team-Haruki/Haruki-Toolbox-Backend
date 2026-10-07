package oauth2

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
)

func TestHydraOAuthManagementEnabledFollowsProvider(t *testing.T) {
	hydraConfig := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{Provider: "hydra"})
	if !HydraOAuthManagementEnabled(hydraConfig) {
		t.Fatalf("expected hydra provider to enable hydra management mode even without admin url")
	}

	disabledConfig := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{Provider: "builtin"})
	if HydraOAuthManagementEnabled(disabledConfig) {
		t.Fatalf("expected builtin provider to disable hydra management mode")
	}
}

func TestListHydraConsentSessionsPaginates(t *testing.T) {
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

	hydraConfig := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{
		AdminURL:       server.URL,
		RequestTimeout: 5 * time.Second,
	})

	sessions, err := ListHydraConsentSessions(context.Background(), hydraConfig, "u-1")
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/oauth2/auth/sessions/consent" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"consent_request_id":"c1","grant_scope":["user:read"],"consent_request":{"client":{"client_id":"client-a","client_name":"Client A","token_endpoint_auth_method":"none"}}}]`))
	}))
	defer server.Close()

	hydraConfig := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{
		AdminURL:       server.URL,
		RequestTimeout: 5 * time.Second,
	})

	exists, err := HydraConsentSessionExistsForClient(context.Background(), hydraConfig, "u-1", "client-a")
	if err != nil {
		t.Fatalf("HydraConsentSessionExistsForClient returned error: %v", err)
	}
	if !exists {
		t.Fatalf("expected client-a to exist")
	}

	exists, err = HydraConsentSessionExistsForClient(context.Background(), hydraConfig, "u-1", "client-b")
	if err != nil {
		t.Fatalf("HydraConsentSessionExistsForClient returned error: %v", err)
	}
	if exists {
		t.Fatalf("expected client-b to be absent")
	}
}

func TestListHydraConsentSessionsForSubjectsDeduplicates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/oauth2/auth/sessions/consent" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("subject") {
		case "kratos-1":
			_, _ = w.Write([]byte(`[{"consent_request_id":"c-shared","grant_scope":["user:read"],"consent_request":{"client":{"client_id":"client-a","client_name":"Client A","token_endpoint_auth_method":"none"}}}]`))
		case "u-1":
			_, _ = w.Write([]byte(`[{"consent_request_id":"c-shared","grant_scope":["user:read"],"consent_request":{"client":{"client_id":"client-a","client_name":"Client A","token_endpoint_auth_method":"none"}}},{"consent_request_id":"c-legacy","grant_scope":["bindings:read"],"consent_request":{"client":{"client_id":"client-b","client_name":"Client B","token_endpoint_auth_method":"client_secret_basic"}}}]`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	hydraConfig := harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{
		AdminURL:       server.URL,
		RequestTimeout: 5 * time.Second,
	})

	sessions, err := ListHydraConsentSessionsForSubjects(context.Background(), hydraConfig, []string{"kratos-1", "u-1"})
	if err != nil {
		t.Fatalf("ListHydraConsentSessionsForSubjects returned error: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("len(sessions) = %d, want 2", len(sessions))
	}
	if sessions[0].ConsentRequestID != "c-shared" {
		t.Fatalf("first consent request id = %q, want %q", sessions[0].ConsentRequestID, "c-shared")
	}
	if sessions[1].ConsentRequestID != "c-legacy" {
		t.Fatalf("second consent request id = %q, want %q", sessions[1].ConsentRequestID, "c-legacy")
	}

	exists, err := HydraConsentSessionExistsForSubjects(context.Background(), hydraConfig, []string{"kratos-1", "u-1"}, "client-b")
	if err != nil {
		t.Fatalf("HydraConsentSessionExistsForSubjects returned error: %v", err)
	}
	if !exists {
		t.Fatalf("expected client-b to exist across subjects")
	}
}

// newHydraConsentRevokeRecorder records the raw query of every consent revocation
// and answers 500 for the subjects in failing, 204 otherwise.
func newHydraConsentRevokeRecorder(t *testing.T, failing ...string) (*harukiOAuth2.HydraConfig, *[]string) {
	t.Helper()
	var mu sync.Mutex
	rawQueries := make([]string, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/oauth2/auth/sessions/consent" || r.Method != http.MethodDelete {
			t.Errorf("unexpected hydra request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		mu.Lock()
		rawQueries = append(rawQueries, r.URL.RawQuery)
		mu.Unlock()
		if slices.Contains(failing, r.URL.Query().Get("subject")) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"server_error"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	return harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{AdminURL: server.URL, RequestTimeout: 5 * time.Second}), &rawQueries
}

func TestRevokeHydraConsentSessionsForSubjectsRevokesAllSubjects(t *testing.T) {
	hydraConfig, rawQueries := newHydraConsentRevokeRecorder(t)

	revoked, failed, err := RevokeHydraConsentSessionsForSubjects(context.Background(), hydraConfig, "client-a", []string{"kratos-1", "u-1", "kratos-1"})
	if err != nil {
		t.Fatalf("RevokeHydraConsentSessionsForSubjects returned error: %v", err)
	}
	if revoked != 2 || len(failed) != 0 {
		t.Fatalf("revoked = %d, failed = %#v, want 2 and none", revoked, failed)
	}
	got := slices.Sorted(slices.Values(*rawQueries))
	if want := []string{"client=client-a&subject=kratos-1", "client=client-a&subject=u-1"}; !slices.Equal(got, want) {
		t.Fatalf("queries = %#v, want %#v", got, want)
	}
}

// Hydra decodes a raw "+" to a space and then revokes nothing (T10).
func TestRevokeEncodesPlusInSubject(t *testing.T) {
	hydraConfig, rawQueries := newHydraConsentRevokeRecorder(t)

	if _, _, err := RevokeHydraConsentSessionsForSubjects(context.Background(), hydraConfig, "bot-1", []string{"a+b"}); err != nil {
		t.Fatalf("RevokeHydraConsentSessionsForSubjects returned error: %v", err)
	}
	if want := []string{"client=bot-1&subject=a%2Bb"}; !slices.Equal(*rawQueries, want) {
		t.Fatalf("queries = %#v, want %#v", *rawQueries, want)
	}
}

func TestRevokeHydraConsentSessionsForSubjectsReportsFailedSubjects(t *testing.T) {
	hydraConfig, rawQueries := newHydraConsentRevokeRecorder(t, "u-2")

	revoked, failed, err := RevokeHydraConsentSessionsForSubjects(context.Background(), hydraConfig, "client-a", []string{"u-1", "u-2", "u-3"})
	if err == nil {
		t.Fatalf("expected an error when a subject fails")
	}
	if status := HydraRequestStatusCode(err); status != http.StatusInternalServerError {
		t.Fatalf("HydraRequestStatusCode(err) = %d, want the wrapped hydra 500", status)
	}
	if revoked != 2 || !slices.Equal(failed, []string{"u-2"}) {
		t.Fatalf("revoked = %d, failed = %#v, want 2 and [u-2]", revoked, failed)
	}
	if len(*rawQueries) != 3 {
		t.Fatalf("queries = %#v, want every subject tried after the failure", *rawQueries)
	}
}

func TestRevokeHydraConsentSessionsForSubjectsWithoutClientRevokesEveryClient(t *testing.T) {
	hydraConfig, rawQueries := newHydraConsentRevokeRecorder(t)

	if _, _, err := RevokeHydraConsentSessionsForSubjects(context.Background(), hydraConfig, "", []string{"kratos-1"}); err != nil {
		t.Fatalf("RevokeHydraConsentSessionsForSubjects returned error: %v", err)
	}
	if want := []string{"all=true&subject=kratos-1"}; !slices.Equal(*rawQueries, want) {
		t.Fatalf("queries = %#v, want %#v", *rawQueries, want)
	}

	if _, _, err := RevokeHydraConsentSessionsForSubjects(context.Background(), hydraConfig, "client-a", []string{" ", ""}); err == nil {
		t.Fatalf("expected an error without subjects")
	}
	if len(*rawQueries) != 1 {
		t.Fatalf("queries = %#v, want no request without subjects", *rawQueries)
	}
}

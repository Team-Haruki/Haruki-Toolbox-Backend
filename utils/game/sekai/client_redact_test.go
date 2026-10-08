package sekai

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/redact"
)

const (
	testInheritID       = "FAKEinheritID0002"
	testInheritPassword = "FAKEpassword0002"
)

func newRedactionTestClient(t *testing.T, api string, inheritID string) (*HarukiSekaiClient, *bytes.Buffer) {
	t.Helper()
	client := NewSekaiClientWithConfig(ClientConfig{
		Server:  JP,
		API:     api,
		Inherit: harukiUtils.InheritInformation{InheritID: inheritID, InheritPassword: testInheritPassword},
	})
	var logs bytes.Buffer
	client.logger = harukiLogger.NewLogger("SekaiClient", "DEBUG", &logs)
	return client, &logs
}

func assertNoInheritSecrets(t *testing.T, where, text string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(text, secret) {
			t.Fatalf("%s leaked a secret: %q", where, text)
		}
	}
}

func TestCallAPIRedactsInheritIDOnTransportError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("hijacking not supported")
			return
		}
		if conn, _, err := hj.Hijack(); err == nil {
			_ = conn.Close()
		}
	}))
	defer srv.Close()

	client, logs := newRedactionTestClient(t, srv.URL+"/api", testInheritID)
	_, _, err := client.callAPI(context.Background(), buildInheritPath(JP, testInheritID, true), httpMethodPost, []byte("x"), nil)
	if err == nil {
		t.Fatal("expected a transport error")
	}

	assertNoInheritSecrets(t, "error", err.Error(), testInheritID)
	assertNoInheritSecrets(t, "log", logs.String(), testInheritID)
	if !strings.Contains(logs.String(), "/inherit/user/<redacted>?isExecuteInherit=False") {
		t.Fatalf("log line lost the route context: %q", logs.String())
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 0 {
		t.Fatalf("want APIError{StatusCode: 0}, got %#v", err)
	}
	if apiErr.Endpoint != "/inherit/user/<redacted>?isExecuteInherit=False" {
		t.Fatalf("APIError.Endpoint = %q", apiErr.Endpoint)
	}
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		t.Fatal("the transport *url.Error must stay reachable for classification")
	}
	assertNoInheritSecrets(t, "*url.Error", urlErr.Error(), testInheritID)

	// The retriever wraps this into a DataRetrievalError that is logged and,
	// for known users, written to the upload audit log.
	wrapped := NewDataRetrievalError("run", "init", "client initialization failed", err)
	assertNoInheritSecrets(t, "DataRetrievalError", wrapped.Error(), testInheritID)
}

func TestCallAPIRedactsInheritIDOnNon200(t *testing.T) {
	t.Parallel()

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	// An inherit ID pasted with a space: pattern matching alone would stop at
	// the space, the exact-value scrub must still catch both halves.
	inheritID := "FAKE ID0003"
	client, logs := newRedactionTestClient(t, srv.URL+"/api", inheritID)
	_, status, err := client.callAPI(context.Background(), buildInheritPath(EN, inheritID, true), httpMethodPost, []byte("x"), nil)
	if status != http.StatusForbidden || err == nil {
		t.Fatalf("status=%d err=%v, want 403 with error", status, err)
	}
	if gotPath != "/api/inherit/user/"+inheritID {
		t.Fatalf("upstream must still receive the real inherit path, got %q", gotPath)
	}

	assertNoInheritSecrets(t, "error", err.Error(), "FAKE", "ID0003")
	assertNoInheritSecrets(t, "log", logs.String(), "FAKE", "ID0003")
	if !strings.Contains(err.Error(), "/inherit/user/<redacted>?isExecuteInherit=False&isAdult=True&tAge=16 returned status 403") {
		t.Fatalf("APIError lost the route context: %q", err.Error())
	}
}

// After the inherit resolves the game user ID, every game API path embeds it.
// Log lines and APIError messages carry a per-process fingerprint instead, the
// same one on every line, while the upstream still gets the real path.
func TestCallAPIMasksResolvedGameUserID(t *testing.T) {
	t.Parallel()

	const gameUserID int64 = 99887766554433221
	uid := "99887766554433221"
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client, logs := newRedactionTestClient(t, srv.URL+"/api", testInheritID)
	client.userID = gameUserID
	_, status, err := client.callAPI(context.Background(), homeRefreshPath(gameUserID), httpMethodPut, []byte("x"), nil)
	if status != http.StatusInternalServerError || err == nil {
		t.Fatalf("status=%d err=%v, want 500 with error", status, err)
	}
	if gotPath != "/api/user/"+uid+"/home/refresh" {
		t.Fatalf("upstream must still receive the real path, got %q", gotPath)
	}
	assertNoInheritSecrets(t, "error", err.Error(), uid)
	assertNoInheritSecrets(t, "log", logs.String(), uid)

	wantPath := "/user/" + redact.Fingerprint(uid) + "/home/refresh"
	if !strings.Contains(err.Error(), wantPath) {
		t.Fatalf("APIError = %q, want it to contain %q", err.Error(), wantPath)
	}
	if !strings.Contains(logs.String(), wantPath) {
		t.Fatalf("log = %q, want it to contain %q", logs.String(), wantPath)
	}
}

func TestCallAPIMasksResolvedGameUserIDOnTransportError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
	}))
	defer srv.Close()

	const gameUserID int64 = 11223344556677889
	uid := "11223344556677889"
	client, logs := newRedactionTestClient(t, srv.URL+"/api", testInheritID)
	client.userID = gameUserID
	_, _, err := client.callAPI(context.Background(), suiteInitialPath(JP, gameUserID), httpMethodGet, nil, nil)
	if err == nil {
		t.Fatal("expected a transport error")
	}
	assertNoInheritSecrets(t, "error", err.Error(), uid)
	assertNoInheritSecrets(t, "log", logs.String(), uid)
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		t.Fatal("the transport *url.Error must stay reachable for classification")
	}
	assertNoInheritSecrets(t, "*url.Error", urlErr.Error(), uid)
	if !strings.Contains(urlErr.URL, "/suite/user/"+redact.Fingerprint(uid)) {
		t.Fatalf("*url.Error URL = %q", urlErr.URL)
	}
}

// Before the inherit resolves an ID there is nothing to mask, and the client
// must not invent a fingerprint for user 0.
func TestMaskedIDsEmptyBeforeInherit(t *testing.T) {
	t.Parallel()
	client, _ := newRedactionTestClient(t, "http://127.0.0.1:1/api", testInheritID)
	if ids := client.maskedIDs(); len(ids) != 0 {
		t.Fatalf("maskedIDs() = %v before inherit", ids)
	}
}

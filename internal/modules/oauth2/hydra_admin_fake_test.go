package oauth2

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"

	json "encoding/json/v2"
)

const (
	fakeHydraClientID         = "client-a"
	fakeHydraBrowserURL       = "https://hydra.example.com/oauth2/auth?client_id=client-a&response_type=code&state=s"
	fakeHydraDeviceURL        = "https://hydra.example.com/oauth2/device/verify?client_id=client-a&user_code=%2A%2A%2A%2A"
	fakeHydraLookupFailDetail = "hydra-internal-detail"

	hydraLoginRequestCall   = "GET /admin/oauth2/auth/requests/login"
	hydraLoginAcceptCall    = "PUT /admin/oauth2/auth/requests/login/accept"
	hydraLoginRejectCall    = "PUT /admin/oauth2/auth/requests/login/reject"
	hydraConsentRequestCall = "GET /admin/oauth2/auth/requests/consent"
	hydraConsentAcceptCall  = "PUT /admin/oauth2/auth/requests/consent/accept"
	hydraConsentRejectCall  = "PUT /admin/oauth2/auth/requests/consent/reject"
	hydraClientLookupCall   = "GET /admin/clients/" + fakeHydraClientID
)

// fakeHydraAdmin is a Hydra admin API serving one login request, one consent
// request and one client, recording every call so a test can assert which
// admin writes were (not) issued.
type fakeHydraAdmin struct {
	// requestURL is the request_url of both the login and the consent request.
	requestURL string
	// consentSubject is the subject of the consent request.
	consentSubject string
	// clientStatus is the status of GET /admin/clients/client-a (0 means 200).
	clientStatus int
	// clientMetadata is the client's metadata; nil omits it (an active client).
	clientMetadata map[string]any

	mu     sync.Mutex
	calls  []string
	bodies map[string]map[string]any
}

func (f *fakeHydraAdmin) start(t *testing.T) *harukiOAuth2.HydraConfig {
	t.Helper()
	f.bodies = make(map[string]map[string]any)
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	return harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{
		Provider:       "hydra",
		AdminURL:       server.URL,
		RequestTimeout: 5 * time.Second,
	})
}

func (f *fakeHydraAdmin) serve(w http.ResponseWriter, r *http.Request) {
	call := r.Method + " " + r.URL.Path
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	f.mu.Lock()
	f.calls = append(f.calls, call)
	if body != nil {
		f.bodies[call] = body
	}
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	switch call {
	case hydraLoginRequestCall:
		writeFakeHydraJSON(w, http.StatusOK, map[string]any{
			"challenge":       r.URL.Query().Get("login_challenge"),
			"skip":            false,
			"subject":         "",
			"request_url":     f.requestURL,
			"requested_scope": []string{"openid", "user:read"},
			"client":          map[string]any{"client_id": fakeHydraClientID},
		})
	case hydraConsentRequestCall:
		writeFakeHydraJSON(w, http.StatusOK, map[string]any{
			"challenge":                       r.URL.Query().Get("consent_challenge"),
			"skip":                            false,
			"subject":                         f.consentSubject,
			"request_url":                     f.requestURL,
			"requested_scope":                 []string{"openid", "user:read"},
			"requested_access_token_audience": []string{},
			"client":                          map[string]any{"client_id": fakeHydraClientID},
		})
	case hydraLoginAcceptCall, hydraLoginRejectCall, hydraConsentAcceptCall, hydraConsentRejectCall:
		writeFakeHydraJSON(w, http.StatusOK, map[string]any{"redirect_to": "https://client.example.com/next"})
	case hydraClientLookupCall:
		if f.clientStatus != 0 && f.clientStatus != http.StatusOK {
			writeFakeHydraJSON(w, f.clientStatus, map[string]any{"error": "error", "error_description": fakeHydraLookupFailDetail})
			return
		}
		client := map[string]any{"client_id": fakeHydraClientID, "client_name": "Client A"}
		if f.clientMetadata != nil {
			client["metadata"] = f.clientMetadata
		}
		writeFakeHydraJSON(w, http.StatusOK, client)
	default:
		writeFakeHydraJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
	}
}

func (f *fakeHydraAdmin) called(call string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.calls, call)
}

func (f *fakeHydraAdmin) body(call string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[call]
}

func writeFakeHydraJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.MarshalWrite(w, value)
}

// genericTestResponse is the envelope of an error response. The code tag is
// spelled out here rather than taken from oauth2ErrorCodeData, so the test
// pins the wire name the pages read.
type genericTestResponse struct {
	Status      int    `json:"status"`
	Message     string `json:"message"`
	UpdatedData *struct {
		Code string `json:"code"`
	} `json:"updatedData"`
}

// code returns updatedData.code, or "" when the response has no updatedData.
func (r genericTestResponse) code() string {
	if r.UpdatedData == nil {
		return ""
	}
	return r.UpdatedData.Code
}

func decodeGenericTestResponse(t *testing.T, resp *http.Response) genericTestResponse {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	var decoded genericTestResponse
	if err := json.UnmarshalRead(resp.Body, &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return decoded
}

func inactiveClientMetadata() map[string]any {
	return map[string]any{hydraClientMetadataNamespace: map[string]any{hydraClientActiveKey: false}}
}

package oauth2

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsoncodec"

	"github.com/gofiber/fiber/v3"
)

// newGenericOAuth2TestApp mirrors the production JSON codec and stubs the
// session middleware with a signed-in user u-1 (Kratos identity kratos-1).
func newGenericOAuth2TestApp() *fiber.App {
	app := fiber.New(fiber.Config{JSONEncoder: jsoncodec.Marshal, JSONDecoder: jsoncodec.Unmarshal})
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "u-1")
		c.Locals("identityID", "kratos-1")
		return c.Next()
	})
	return app
}

func doGenericOAuth2Request(t *testing.T, app *fiber.App, method, target, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	return resp
}

func assertDeviceFlowChallengeRefused(t *testing.T, resp *http.Response) {
	t.Helper()
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
	}
	got := decodeGenericTestResponse(t, resp)
	if got.code() != "device_flow_challenge" {
		t.Fatalf("updatedData.code = %q, want device_flow_challenge (%+v)", got.code(), got)
	}
	if got.Message != errHydraDeviceFlowChallenge.Message {
		t.Fatalf("message = %q, want %q", got.Message, errHydraDeviceFlowChallenge.Message)
	}
}

// consentSubjectMismatchMessage is the refusal a signed-in user gets for a
// consent request that belongs to someone else.
const consentSubjectMismatchMessage = "consent request subject does not match current user"

func assertConsentSubjectMismatch(t *testing.T, resp *http.Response) {
	t.Helper()
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
	}
	got := decodeGenericTestResponse(t, resp)
	if got.Message != consentSubjectMismatchMessage {
		t.Fatalf("message = %q, want %q", got.Message, consentSubjectMismatchMessage)
	}
	// No code either: a non-owner must not learn the flow type or the
	// client's state from the response.
	if got.code() != "" {
		t.Fatalf("updatedData.code = %q, want none for a subject mismatch", got.code())
	}
}

func TestIsDeviceFlowRequestURL(t *testing.T) {
	cases := []struct {
		requestURL string
		want       bool
	}{
		{fakeHydraDeviceURL, true},
		{"http://hydra:4444/oauth2/device/verify", true},
		{"https://hydra.example.com/oauth2/device/verify/?client_id=a", true},
		{"https://api.example.com/hydra/oauth2/device/verify?client_id=a#frag", true},
		{"/oauth2/device/verify?client_id=a", true},
		{"  " + fakeHydraDeviceURL + "  ", true},
		// Does not parse (bad escape): the raw path is checked instead.
		{"https://hydra.example.com/%zz/oauth2/device/verify?client_id=a", true},
		{fakeHydraBrowserURL, false},
		{"https://hydra.example.com/oauth2/auth?next=/oauth2/device/verify", false},
		{"https://hydra.example.com/oauth2/device/verify-other", false},
		{"https://hydra.example.com/oauth2/device/verifyx", false},
		{"https://hydra.example.com/oauth2/device", false},
		{"https://hydra.example.com/%zz/oauth2/auth?next=/oauth2/device/verify", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isDeviceFlowRequestURL(tc.requestURL); got != tc.want {
			t.Errorf("isDeviceFlowRequestURL(%q) = %v, want %v", tc.requestURL, got, tc.want)
		}
	}
}

func TestLoginGetRefusesDeviceChallenge(t *testing.T) {
	for _, tc := range []struct {
		name       string
		requestURL string
		wantStatus int
	}{
		{"device", fakeHydraDeviceURL, fiber.StatusForbidden},
		{"browser", fakeHydraBrowserURL, fiber.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hydra := &fakeHydraAdmin{requestURL: tc.requestURL}
			hydraConfig := hydra.start(t)
			app := newGenericOAuth2TestApp()
			app.Get("/api/oauth2/login", handleHydraGetLoginRequest(hydraConfig))

			resp := doGenericOAuth2Request(t, app, http.MethodGet, "/api/oauth2/login?login_challenge=lc", "")
			if tc.wantStatus == fiber.StatusForbidden {
				assertDeviceFlowChallengeRefused(t, resp)
				return
			}
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
		})
	}
}

func TestLoginAcceptRefusesDeviceChallenge(t *testing.T) {
	hydra := &fakeHydraAdmin{requestURL: fakeHydraDeviceURL}
	hydraConfig := hydra.start(t)
	app := newGenericOAuth2TestApp()
	app.Post("/api/oauth2/login/accept", handleHydraAcceptLogin(hydraConfig))

	resp := doGenericOAuth2Request(t, app, http.MethodPost, "/api/oauth2/login/accept", `{"loginChallenge":"lc","remember":false}`)
	assertDeviceFlowChallengeRefused(t, resp)
	if !hydra.called(hydraLoginRequestCall) {
		t.Fatalf("login request was not read before deciding")
	}
	if hydra.called(hydraLoginAcceptCall) {
		t.Fatalf("login accept must not be sent for a device challenge")
	}
}

func TestConsentRefusesDeviceChallenge(t *testing.T) {
	routes := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{"get", http.MethodGet, "/api/oauth2/consent?consent_challenge=cc", ""},
		{"accept", http.MethodPost, "/api/oauth2/consent/accept", `{"consentChallenge":"cc","grantScope":["user:read"]}`},
		{"legacy approve", http.MethodPost, "/api/oauth2/authorize/consent", `{"consentChallenge":"cc","approved":true,"scope":"user:read"}`},
		{"reject", http.MethodPost, "/api/oauth2/consent/reject", `{"consentChallenge":"cc"}`},
		{"legacy reject", http.MethodPost, "/api/oauth2/authorize/consent", `{"consentChallenge":"cc","approved":false}`},
	}
	owners := []struct {
		name    string
		subject string
		assert  func(*testing.T, *http.Response)
	}{
		// The request belongs to the signed-in user, so only the device check
		// stands between the request and Hydra.
		{"owner", "kratos-1", assertDeviceFlowChallengeRefused},
		// Someone else's request: the subject check runs first, so the
		// response does not reveal that the request is a device flow.
		{"not owner", "someone-else", assertConsentSubjectMismatch},
	}
	for _, route := range routes {
		for _, owner := range owners {
			t.Run(route.name+"/"+owner.name, func(t *testing.T) {
				hydra := &fakeHydraAdmin{requestURL: fakeHydraDeviceURL, consentSubject: owner.subject}
				hydraConfig := hydra.start(t)
				apiHelper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
				app := newGenericOAuth2TestApp()
				app.Get("/api/oauth2/consent", handleHydraGetConsentRequest(hydraConfig))
				app.Post("/api/oauth2/consent/accept", handleHydraAcceptConsent(apiHelper, hydraConfig))
				app.Post("/api/oauth2/consent/reject", handleHydraRejectConsent(hydraConfig))
				app.Post("/api/oauth2/authorize/consent", handleHydraLegacyConsentDecision(apiHelper, hydraConfig))

				resp := doGenericOAuth2Request(t, app, route.method, route.target, route.body)
				owner.assert(t, resp)
				if hydra.called(hydraConsentAcceptCall) {
					t.Fatalf("consent accept must not be sent for a device challenge")
				}
				if hydra.called(hydraConsentRejectCall) {
					t.Fatalf("consent reject must not be sent for a device challenge")
				}
				if hydra.called(hydraClientLookupCall) {
					t.Fatalf("device challenge should be refused before the client lookup")
				}
			})
		}
	}
}

func TestConsentRejectRefusesDeviceChallenge(t *testing.T) {
	routes := []struct {
		name       string
		target     string
		body       string
		rejectCall string
	}{
		{"consent reject", "/api/oauth2/consent/reject", `{"consentChallenge":"cc"}`, hydraConsentRejectCall},
		{"legacy reject", "/api/oauth2/authorize/consent", `{"consentChallenge":"cc","approved":false}`, hydraConsentRejectCall},
		{"login reject", "/api/oauth2/login/reject", `{"loginChallenge":"lc"}`, hydraLoginRejectCall},
	}
	for _, route := range routes {
		for _, flow := range []struct {
			name        string
			requestURL  string
			wantRefusal bool
		}{
			{"device", fakeHydraDeviceURL, true},
			{"browser", fakeHydraBrowserURL, false},
		} {
			t.Run(route.name+"/"+flow.name, func(t *testing.T) {
				hydra := &fakeHydraAdmin{requestURL: flow.requestURL, consentSubject: "kratos-1"}
				hydraConfig := hydra.start(t)
				apiHelper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
				app := newGenericOAuth2TestApp()
				app.Post("/api/oauth2/consent/reject", handleHydraRejectConsent(hydraConfig))
				app.Post("/api/oauth2/authorize/consent", handleHydraLegacyConsentDecision(apiHelper, hydraConfig))
				app.Post("/api/oauth2/login/reject", handleHydraRejectLogin(hydraConfig))

				resp := doGenericOAuth2Request(t, app, http.MethodPost, route.target, route.body)
				if flow.wantRefusal {
					assertDeviceFlowChallengeRefused(t, resp)
					if hydra.called(route.rejectCall) {
						t.Fatalf("reject must not be sent for a device challenge")
					}
					return
				}
				if resp.StatusCode != fiber.StatusOK {
					t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
				}
				if !hydra.called(route.rejectCall) {
					t.Fatalf("reject was not forwarded for a browser challenge")
				}
			})
		}
	}
}

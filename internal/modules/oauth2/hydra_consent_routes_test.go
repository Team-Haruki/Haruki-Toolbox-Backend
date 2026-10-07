package oauth2

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"

	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

var consentAcceptRoutes = []struct {
	name   string
	target string
	body   string
}{
	{"accept", "/api/oauth2/consent/accept", `{"consentChallenge":"cc","grantScope":["openid","user:read"]}`},
	{"legacy approve", "/api/oauth2/authorize/consent", `{"consentChallenge":"cc","approved":true,"scope":"openid user:read"}`},
}

func newConsentAcceptTestApp(t *testing.T, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, hydra *fakeHydraAdmin) *fiber.App {
	t.Helper()
	hydraConfig := hydra.start(t)
	app := newGenericOAuth2TestApp()
	app.Post("/api/oauth2/consent/accept", handleHydraAcceptConsent(apiHelper, hydraConfig))
	app.Post("/api/oauth2/authorize/consent", handleHydraLegacyConsentDecision(apiHelper, hydraConfig))
	return app
}

func TestConsentAcceptRejectsInactiveClient(t *testing.T) {
	clients := []struct {
		name        string
		status      int
		metadata    map[string]any
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{"disabled", http.StatusOK, inactiveClientMetadata(), fiber.StatusForbidden, "client_disabled", errHydraConsentClientDisabled.Message},
		// A deleted client is indistinguishable from a disabled one.
		{"deleted", http.StatusNotFound, nil, fiber.StatusForbidden, "client_disabled", errHydraConsentClientDisabled.Message},
		// A failed lookup refuses without echoing Hydra's error text, with
		// the bearer middleware's wording and no code.
		{"lookup failed", http.StatusInternalServerError, nil, fiber.StatusServiceUnavailable, "", "oauth2 client validation unavailable"},
	}
	for _, route := range consentAcceptRoutes {
		for _, client := range clients {
			t.Run(route.name+"/"+client.name, func(t *testing.T) {
				hydra := &fakeHydraAdmin{
					requestURL:     fakeHydraBrowserURL,
					consentSubject: "kratos-1",
					clientStatus:   client.status,
					clientMetadata: client.metadata,
				}
				// No database: the refusal must come before the user lookup.
				app := newConsentAcceptTestApp(t, &harukiAPIHelper.HarukiToolboxRouterHelpers{}, hydra)

				resp := doGenericOAuth2Request(t, app, http.MethodPost, route.target, route.body)
				if resp.StatusCode != client.wantStatus {
					t.Fatalf("status = %d, want %d", resp.StatusCode, client.wantStatus)
				}
				got := decodeGenericTestResponse(t, resp)
				if got.code() != client.wantCode {
					t.Fatalf("updatedData.code = %q, want %q", got.code(), client.wantCode)
				}
				if got.Message != client.wantMessage {
					t.Fatalf("message = %q, want %q", got.Message, client.wantMessage)
				}
				if strings.Contains(got.Message, fakeHydraLookupFailDetail) {
					t.Fatalf("response leaked Hydra error text: %q", got.Message)
				}
				if !hydra.called(hydraClientLookupCall) {
					t.Fatalf("client was not looked up")
				}
				if hydra.called(hydraConsentAcceptCall) {
					t.Fatalf("consent accept must not be sent for an unavailable client")
				}
			})
		}
		// Someone else's request: the subject check runs before the client
		// lookup, so the response does not reveal the client's state.
		t.Run(route.name+"/not owner", func(t *testing.T) {
			hydra := &fakeHydraAdmin{
				requestURL:     fakeHydraBrowserURL,
				consentSubject: "someone-else",
				clientMetadata: inactiveClientMetadata(),
			}
			app := newConsentAcceptTestApp(t, &harukiAPIHelper.HarukiToolboxRouterHelpers{}, hydra)

			resp := doGenericOAuth2Request(t, app, http.MethodPost, route.target, route.body)
			assertConsentSubjectMismatch(t, resp)
			if hydra.called(hydraClientLookupCall) {
				t.Fatalf("client was looked up before the subject check")
			}
			if hydra.called(hydraConsentAcceptCall) {
				t.Fatalf("consent accept must not be sent for someone else's request")
			}
		})
	}
}

func TestConsentAcceptAllowsActiveClient(t *testing.T) {
	db := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", strings.ReplaceAll(t.Name(), "/", "-")))
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.User.Create().SetID("u-1").SetName("User One").SetEmail("u1@example.com").Save(context.Background()); err != nil {
		t.Fatalf("create user: %v", err)
	}
	apiHelper := &harukiAPIHelper.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: db}}

	for _, route := range consentAcceptRoutes {
		t.Run(route.name, func(t *testing.T) {
			hydra := &fakeHydraAdmin{
				requestURL:     fakeHydraBrowserURL,
				consentSubject: "kratos-1",
				clientMetadata: map[string]any{hydraClientMetadataNamespace: map[string]any{hydraClientActiveKey: true}},
			}
			app := newConsentAcceptTestApp(t, apiHelper, hydra)

			resp := doGenericOAuth2Request(t, app, http.MethodPost, route.target, route.body)
			if resp.StatusCode != fiber.StatusOK {
				t.Fatalf("status = %d, want %d (%+v)", resp.StatusCode, fiber.StatusOK, decodeGenericTestResponse(t, resp))
			}
			accept := hydra.body(hydraConsentAcceptCall)
			if accept == nil {
				t.Fatalf("consent accept was not sent")
			}
			if scope, _ := accept["grant_scope"].([]any); len(scope) != 2 || scope[0] != "openid" || scope[1] != "user:read" {
				t.Fatalf("grant_scope = %#v", accept["grant_scope"])
			}
		})
	}
}

package oauth2

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestLoginAcceptIgnoresBrowserACR(t *testing.T) {
	hydra := &fakeHydraAdmin{requestURL: fakeHydraBrowserURL}
	hydraConfig := hydra.start(t)
	app := newGenericOAuth2TestApp()
	app.Post("/api/oauth2/login/accept", handleHydraAcceptLogin(hydraConfig))

	resp := doGenericOAuth2Request(t, app, http.MethodPost, "/api/oauth2/login/accept", `{"loginChallenge":"lc","remember":true,"rememberFor":3600,"acr":"aal2"}`)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}

	accept := hydra.body(hydraLoginAcceptCall)
	if accept == nil {
		t.Fatalf("login accept was not sent")
	}
	if _, ok := accept["acr"]; ok {
		t.Fatalf("login accept forwarded the browser-supplied acr: %#v", accept)
	}
	if accept["subject"] != "kratos-1" || accept["remember"] != true || accept["remember_for"] != float64(3600) {
		t.Fatalf("login accept body = %#v", accept)
	}
	if len(accept) != 3 {
		t.Fatalf("login accept body has unexpected keys: %#v", accept)
	}
}

package upload

import (
	"github.com/gofiber/fiber/v3"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserUploadMetadata(t *testing.T) {
	for _, tc := range []struct{ ua, platform string }{{"Android Linux", "android"}, {"iPhone", "ios"}, {"iPad", "ios"}, {"Windows NT", "windows"}, {"Macintosh", "macos"}, {"Linux", "linux"}, {"arbitrary UA", "unknown"}} {
		app := fiber.New()
		app.Post("/", func(c fiber.Ctx) error {
			a := browserUploadAttempt(c)
			if a.Client.Platform != tc.platform || a.RequestBytes != 3 {
				t.Fatalf("unexpected attempt %+v", a)
			}
			return c.SendStatus(200)
		})
		req := httptest.NewRequest("POST", "/", strings.NewReader("abc"))
		req.Header.Set("User-Agent", tc.ua)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
}
func TestOAuthUploadMetadataUsesTrustedLocal(t *testing.T) {
	for _, id := range []string{"client", strings.Repeat("x", 256), ""} {
		app := fiber.New()
		app.Post("/", func(c fiber.Ctx) error {
			c.Locals("oauth2ClientID", id)
			a := oauthUploadAttempt(c)
			want := id
			if len(id) > 255 {
				want = ""
			}
			if a.Client.OAuthClientID != want {
				t.Fatal(a.Client.OAuthClientID)
			}
			return c.SendStatus(200)
		})
		req := httptest.NewRequest("POST", "/", nil)
		req.Header.Set("X-OAuth-Client-ID", "forged")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
}

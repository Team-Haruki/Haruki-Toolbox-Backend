package upload

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// The synchronous route executed a transfer without handing the account
// back. It now refuses every request, on both servers, before reading the
// body or touching any dependency.
func TestInheritSubmitRouteIsGoneForEveryServer(t *testing.T) {
	for _, server := range []string{"jp", "en"} {
		for _, dataType := range []string{"suite", "mysekai"} {
			t.Run(server+"/"+dataType, func(t *testing.T) {
				app := fiber.New()
				app.Post("/api/inherit/:server/:upload_type/submit", handleInheritSubmitGone)
				req := httptest.NewRequest("POST", "/api/inherit/"+server+"/"+dataType+"/submit", strings.NewReader(`{"inherit_id":"x","inherit_password":"y"}`))
				req.Header.Set("Content-Type", "application/json")
				res, err := app.Test(req)
				if err != nil {
					t.Fatal(err)
				}
				defer res.Body.Close()
				body, _ := io.ReadAll(res.Body)
				if res.StatusCode != fiber.StatusGone || !strings.Contains(string(body), "client_update_required") || !strings.Contains(string(body), "重新生成新引继码") {
					t.Fatalf("status=%d body=%s", res.StatusCode, body)
				}
			})
		}
	}
}

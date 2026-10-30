package upload

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestJPInheritSuspensionBeforeCredentialsAndDependencies(t *testing.T) {
	for _, dataType := range []string{"suite", "mysekai"} {
		t.Run(dataType, func(t *testing.T) {
			app := fiber.New()
			app.Post("/api/inherit/:server/:upload_type/submit", handleInheritSubmit(nil, Dependencies{}))
			req := httptest.NewRequest("POST", "/api/inherit/jp/"+dataType+"/submit", strings.NewReader("invalid json"))
			req.Header.Set("Content-Type", "application/json")
			res, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != 503 || !strings.Contains(string(body), "重新生成新引继码") {
				t.Fatalf("status=%d body=%s", res.StatusCode, body)
			}
		})
	}
}

func TestENInheritStillValidatesPayload(t *testing.T) {
	app := fiber.New()
	app.Post("/api/inherit/:server/:upload_type/submit", handleInheritSubmit(nil, Dependencies{}))
	req := httptest.NewRequest("POST", "/api/inherit/en/suite/submit", strings.NewReader("invalid json"))
	req.Header.Set("Content-Type", "application/json")
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("status=%d, want 400", res.StatusCode)
	}
}

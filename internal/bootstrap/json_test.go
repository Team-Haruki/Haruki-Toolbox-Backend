package bootstrap

import (
	"net/http/httptest"
	"strings"
	"testing"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	"github.com/gofiber/fiber/v3"
)

func TestFiberJSONV2RejectsDuplicateMembers(t *testing.T) {
	app, closeLog, err := newFiberApp(harukiConfig.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeLog()
	app.Post("/json-contract", func(c fiber.Ctx) error {
		var body struct {
			Value int `json:"value"`
		}
		if err := c.Bind().JSON(&body); err != nil {
			return c.SendStatus(400)
		}
		return c.JSON(body)
	})
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"value":1,"value":2}`, 400},
		{`{"value":2}`, 200},
	} {
		request := httptest.NewRequest("POST", "/json-contract", strings.NewReader(tc.body))
		request.Header.Set("Content-Type", "application/json")
		response, err := app.Test(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != tc.status {
			t.Fatalf("status=%d want=%d", response.StatusCode, tc.status)
		}
	}
}

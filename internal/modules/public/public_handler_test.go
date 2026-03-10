package public

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestHandlePublicDataRequestInvalidParamsReturnBadRequest(t *testing.T) {
	app := fiber.New()
	apiHelper := &harukiAPIHelper.HarukiToolboxRouterHelpers{}
	app.Get("/public/:server/:data_type/:user_id", handlePublicDataRequest(apiHelper))

	testCases := []struct {
		name string
		path string
	}{
		{
			name: "invalid server",
			path: "/public/invalid/suite/123",
		},
		{
			name: "invalid data type",
			path: "/public/jp/invalid/123",
		},
		{
			name: "invalid user id",
			path: "/public/jp/suite/not-int",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test returned error: %v", err)
			}
			if resp.StatusCode != fiber.StatusBadRequest {
				t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
			}
		})
	}
}

package usergamebindings

import (
	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestParseOwnedGameAccountDataType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		raw       string
		want      ownedGameAccountDataType
		wantError bool
	}{
		{name: "suite", raw: "suite", want: ownedGameAccountDataTypeSuite},
		{name: "mysekai trims and lowercases", raw: " MySekai ", want: ownedGameAccountDataTypeMysekai},
		{name: "profile", raw: "profile", want: ownedGameAccountDataTypeProfile},
		{name: "empty", raw: "", wantError: true},
		{name: "birthday party not allowed", raw: "mysekai_birthday_party", wantError: true},
		{name: "unknown", raw: "all", wantError: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseOwnedGameAccountDataType(tc.raw)
			if tc.wantError {
				if err == nil || err.Code != fiber.StatusBadRequest {
					t.Fatalf("expected bad request error, got %#v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("data type = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGameAccountDataRouteRejectsMismatchedUserID(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Get("/api/user/:toolbox_user_id/game-account/:server/:game_user_id/:data_type",
		func(c fiber.Ctx) error {
			c.Locals("userID", "u-100")
			return c.Next()
		},
		userCoreModule.RequireSelfUserParam("toolbox_user_id"),
		func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) },
	)

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/api/user/u-200/game-account/jp/123/suite", nil))
	if err != nil {
		t.Fatalf("app.Test returned error: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusForbidden)
	}
}

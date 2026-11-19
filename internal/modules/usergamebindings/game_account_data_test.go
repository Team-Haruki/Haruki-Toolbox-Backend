package usergamebindings

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	userCoreModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/usercore"
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/runtimeconfig"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	"github.com/redis/go-redis/v9"
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

func TestOwnedGameAccountConditionalReadIgnoresMissingOrInvalidStamp(t *testing.T) {
	t.Parallel()

	for _, query := range []string{"", "?known_upload_time=invalid", "?known_upload_time=0"} {
		query := query
		t.Run(query, func(t *testing.T) {
			t.Parallel()
			app := fiber.New()
			app.Get("/data", func(c fiber.Ctx) error {
				if ownedGameAccountNotModified(
					context.Background(),
					c,
					nil,
					123,
					harukiUtils.SupportedDataUploadServerJP,
					harukiUtils.UploadDataTypeSuite,
					"",
					true,
					[]string{"upload_time"},
				) {
					t.Fatal("invalid or missing stamp must not produce 304")
				}
				return c.SendStatus(fiber.StatusOK)
			})

			resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/data"+query, nil))
			if err != nil {
				t.Fatalf("app.Test returned error: %v", err)
			}
			if resp.StatusCode != fiber.StatusOK {
				t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
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

func TestOwnedMysekaiReadUsesCurrentFieldPolicy(t *testing.T) {
	db := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer db.Close()
	db.User.Create().SetID("owner").SetName("owner").SetEmail("owner@example.test").ExecX(t.Context())
	db.GameAccountBinding.Create().SetServer("jp").SetGameUserID("123").SetVerified(true).SetUserID("owner").ExecX(t.Context())
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	manager := &harukiRedis.HarukiRedisManager{Redis: client}
	helper := &api.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{DB: db, Redis: manager},
		RuntimeConfig: runtimeconfig.New(
			runtimeconfig.Snapshot{AllowedKeys: []string{"userMysekaiGates"}},
			runtimeconfig.NewRedisStore(manager),
		),
	}
	app := fiber.New()
	app.Get("/:server/:game_user_id/:data_type", func(c fiber.Ctx) error {
		c.Locals("userID", "owner")
		return c.Next()
	}, handleGetOwnedGameAccountData(helper))
	for _, tc := range []struct {
		policy  string
		status  int
		message string
	}{
		{`{"publicApiAllowedKeys":["upload_time"]}`, http.StatusBadRequest, "Invalid request key"},
		{`{}`, http.StatusBadRequest, "Invalid request key"},
		{"unreadable policy", http.StatusServiceUnavailable, "game-data field policy unavailable"},
	} {
		if err := server.Set(harukiRedis.BuildRuntimeConfigKey(), tc.policy); err != nil {
			t.Fatal(err)
		}
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/jp/123/mysekai?key=userMysekaiGates", nil))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != tc.status || !strings.Contains(string(body), tc.message) {
			t.Fatalf("policy %q returned %d: %s, %v", tc.policy, resp.StatusCode, body, err)
		}
	}
}

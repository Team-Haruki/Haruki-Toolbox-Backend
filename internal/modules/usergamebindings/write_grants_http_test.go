package usergamebindings

import (
	"context"
	"encoding/json"
	"fmt"
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/gofiber/fiber/v3"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWriteGrantHTTPAndDiscovery(t *testing.T) {
	db := enttest.Open(t, "sqlite3", "file:grant-http-write?mode=memory&cache=shared&_fk=1")
	defer db.Close()
	ctx := context.Background()
	for _, id := range []string{"owner", "actor"} {
		if _, err := db.User.Create().SetID(id).SetName(id).SetEmail(id + "@example.com").Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.GameAccountBinding.Create().SetServer("jp").SetGameUserID("123").SetVerified(true).SetUserID("owner").Save(ctx); err != nil {
		t.Fatal(err)
	}
	helper := &api.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: db}}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		if c.Method() == "PUT" {
			c.Locals("userID", "owner")
		} else {
			c.Locals("userID", "actor")
		}
		return c.Next()
	})
	app.Put("/:server/:game_user_id/:data_type/:grantee_user_id", handleUpsertGameAccountDataGrant(helper))
	app.Get("/accounts", handleListAccessibleGameAccounts(helper))
	app.Get("/grants", handleListReceivedGameAccountDataGrants(helper))
	for _, tc := range []struct {
		permissions string
		status      int
	}{{`["write"]`, 200}, {`[]`, 400}, {`["unknown"]`, 400}, {`null`, 200}} {
		body := fmt.Sprintf(`{"expiresAt":%q,"permissions":%s}`, time.Now().Add(time.Hour).Format(time.RFC3339), tc.permissions)
		req := httptest.NewRequest("PUT", "/jp/123/suite/actor", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Fatal(resp.StatusCode)
		}
	}
	for _, action := range []string{"write", "read", "invalid"} {
		resp, err := app.Test(httptest.NewRequest("GET", "/accounts?action="+action, nil))
		if err != nil {
			t.Fatal(err)
		}
		if action == "invalid" {
			resp.Body.Close()
			if resp.StatusCode != 400 {
				t.Fatal(resp.StatusCode)
			}
			continue
		}
		var body struct {
			Data accessibleGameAccountListResponse `json:"updatedData"`
		}
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if action == "read" && len(body.Data.Accounts) != 0 {
			t.Fatal("write grant leaked into read selector")
		}
		if action == "write" {
			if len(body.Data.Accounts) != 1 {
				t.Fatal(body)
			}
			a := body.Data.Accounts[0]
			if _, ok := a.WriteCapabilities["suite"]; !ok || len(a.Capabilities) != 0 || a.Owner != nil {
				t.Fatal(a)
			}
		}
	}
	resp, err := app.Test(httptest.NewRequest("GET", "/grants", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Data gameAccountDataGrantListResponse `json:"updatedData"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Items) != 1 || len(body.Data.Items[0].Permissions) != 1 || body.Data.Items[0].Permissions[0] != "write" {
		t.Fatal(body)
	}
}

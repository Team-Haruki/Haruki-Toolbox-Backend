package admincontent

import (
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

func TestFriendLinkWritesAndListsEnforceTagBudgets(t *testing.T) {
	db := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer db.Close()
	ctx := t.Context()
	db.FriendLink.Create().SetName("legacy").SetDescription("legacy fixture").SetURL("https://example.test").SetAvatar("").
		SetTags([]string{strings.Repeat("x", 16<<10)}).SetSortOrder(1).ExecX(ctx)
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: db}}
	app := fiber.New()
	app.Get("/links", handleAdminListFriendLinks(helper))
	app.Post("/links", handleAdminCreateFriendLink(helper))
	app.Put("/links/:id", handleAdminUpdateFriendLink(helper))
	request := func(method, path string, tags []string, want int) {
		t.Helper()
		payload, err := json.Marshal(adminFriendLinkPayload{Name: "fixture", Description: "desc", URL: "https://example.test", Tags: tags, SortOrder: 2})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, path, strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("%s %s returned %d, want %d", method, path, resp.StatusCode, want)
		}
	}
	request(http.MethodPost, "/links", make([]string, postgresql.MaxFriendLinkTags+1), http.StatusBadRequest)
	request(http.MethodPut, "/links/1", []string{strings.Repeat("节", 45)}, http.StatusBadRequest)
	request(http.MethodPost, "/links", []string{"tag", "small"}, http.StatusOK)
	for _, tc := range []struct {
		path, name, tag string
		next            int
	}{
		{"/links?page_size=1", "legacy", "", 2}, {"/links?page=2&page_size=1", "fixture", "tag", 0},
	} {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, tc.path, nil))
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Data adminFriendLinksResponse `json:"updatedData"`
		}
		err = json.UnmarshalRead(resp.Body, &body)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK || body.Data.Total != 2 || body.Data.NextPage != tc.next || len(body.Data.Items) != 1 {
			t.Fatalf("unexpected friend link page %+v: %v", body.Data, err)
		}
		got := body.Data.Items[0]
		if got.Name != tc.name || (tc.tag == "" && len(got.Tags) != 0) || (tc.tag != "" && (len(got.Tags) == 0 || got.Tags[0] != tc.tag)) {
			t.Fatal("legacy link was lost or its unbounded tags were returned")
		}
	}
}

func TestParseAdminFriendLinkPayload(t *testing.T) {
	app := fiber.New()
	app.Post("/", func(c fiber.Ctx) error {
		payload, err := parseAdminFriendLinkPayload(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.Status(fiberErr.Code).SendString(fiberErr.Message)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendString(payload.Name + "|" + payload.Description + "|" + payload.Avatar + "|" + payload.URL + "|" + strings.Join(payload.Tags, ","))
	})

	t.Run("valid payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"友情链接","description":"desc","avatar":"a.png","url":"https://example.com","tags":[" tech ","","tech","go"]}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("io.ReadAll returned error: %v", err)
		}
		if string(body) != "友情链接|desc|a.png|https://example.com|tech,go" {
			t.Fatalf("response body = %q", string(body))
		}
	})

	t.Run("missing name", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"description":"desc","url":"https://example.com"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}

func TestParseAdminFriendGroupPayload(t *testing.T) {
	app := fiber.New()
	app.Post("/", func(c fiber.Ctx) error {
		payload, err := parseAdminFriendGroupPayload(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.Status(fiberErr.Code).SendString(fiberErr.Message)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}
		return c.SendString(payload.Group)
	})

	t.Run("valid payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"group":"  工具站  "}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("io.ReadAll returned error: %v", err)
		}
		if string(body) != "工具站" {
			t.Fatalf("response body = %q, want 工具站", string(body))
		}
	})

	t.Run("empty group", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"group":" "}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("accept name alias", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"  推荐群聊  "}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("io.ReadAll returned error: %v", err)
		}
		if string(body) != "推荐群聊" {
			t.Fatalf("response body = %q, want 推荐群聊", string(body))
		}
	})

	t.Run("unicode rune limit", func(t *testing.T) {
		validGroup := strings.Repeat("你", 64)
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"group":"`+validGroup+`"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}

		invalidGroup := strings.Repeat("你", 65)
		req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"group":"`+invalidGroup+`"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err = app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})
}

func TestParseAdminFriendGroupItemPayload(t *testing.T) {
	app := fiber.New()
	app.Post("/", func(c fiber.Ctx) error {
		payload, err := parseAdminFriendGroupItemPayload(c)
		if err != nil {
			if fiberErr, ok := err.(*fiber.Error); ok {
				return c.Status(fiberErr.Code).SendString(fiberErr.Message)
			}
			return c.SendStatus(fiber.StatusBadRequest)
		}

		avatar := "-"
		bg := "-"
		if payload.Avatar != nil {
			avatar = *payload.Avatar
		}
		if payload.Bg != nil {
			bg = *payload.Bg
		}
		return c.SendString(payload.Name + "|" + avatar + "|" + bg + "|" + payload.GroupInfo + "|" + payload.Detail)
	})

	t.Run("valid payload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"A","avatar":"  aa.png ","bg":"", "groupInfo":"GI","detail":"detail"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("io.ReadAll returned error: %v", err)
		}
		if string(body) != "A|aa.png||GI|detail" {
			t.Fatalf("response body = %q, want %q", string(body), "A|aa.png||GI|detail")
		}
	})

	t.Run("missing required", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"A","groupInfo":"","detail":"detail"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusBadRequest {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
		}
	})

	t.Run("accept snake and description aliases", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"A","group_info":"GI","description":"detail"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test returned error: %v", err)
		}
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("status code = %d, want %d", resp.StatusCode, fiber.StatusOK)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("io.ReadAll returned error: %v", err)
		}
		if string(body) != "A|-|-|GI|detail" {
			t.Fatalf("response body = %q, want %q", string(body), "A|-|-|GI|detail")
		}
	})
}

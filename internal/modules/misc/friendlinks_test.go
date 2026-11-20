package misc

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

func TestPublicFriendLinksKeepOrderedBoundedPages(t *testing.T) {
	db := enttest.Open(t, "sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer db.Close()
	db.FriendLink.Create().SetName("second").SetDescription("fixture").SetAvatar("").SetURL("https://example.test").
		SetSortOrder(2).SetTags(make([]string, postgresql.MaxFriendLinkTags+1)).ExecX(t.Context())
	db.FriendLink.Create().SetName("first").SetDescription("fixture").SetAvatar("").SetURL("https://example.test").
		SetSortOrder(1).SetTags([]string{"normal"}).ExecX(t.Context())
	app := fiber.New()
	registerFriendLinksRoutes(&api.HarukiToolboxRouterHelpers{Router: app, DBManager: &database.HarukiToolboxDBManager{DB: db}}, AssetsConfig{})
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/misc/friend_links?page_size=201", nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized friend link page returned %d, want 400", resp.StatusCode)
	}
	for _, want := range []struct {
		query, name, next string
		tags              int
	}{
		{"page_size=1", "first", "2", 1}, {"page=2&page_size=1", "second", "", 0},
	} {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/misc/friend_links?"+want.query, nil))
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Links []FriendLinkData `json:"updatedData"`
		}
		err = json.UnmarshalRead(resp.Body, &envelope)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("X-Next-Page") != want.next || len(envelope.Links) != 1 {
			t.Fatalf("unexpected page %s: status=%d links=%v err=%v", want.query, resp.StatusCode, envelope.Links, err)
		}
		if got := envelope.Links[0]; got.Name != want.name || len(got.Tags) != want.tags {
			t.Fatalf("friend link page bypassed ordering or tag budget: %v", got)
		}
	}
}

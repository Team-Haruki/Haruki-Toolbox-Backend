package oauth2

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api/data"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/runtimeconfig"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/gofiber/fiber/v3"

	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
)

func TestOAuth2GameDataGrantableTypes(t *testing.T) {
	t.Parallel()

	if !postgresql.IsGrantableGameAccountDataType("suite") {
		t.Fatalf("suite should be grantable")
	}
	if !postgresql.IsGrantableGameAccountDataType(" MySekai ") {
		t.Fatalf("mysekai should be grantable")
	}
	if !postgresql.IsGrantableGameAccountDataType("profile") {
		t.Fatalf("profile should be grantable")
	}
	// The OAuth2 surface still cannot ask for profile: it parses the data type
	// with ParseUploadDataType, which knows only the stored upload types. Making
	// profile grantable therefore widens the browser surface only, and does not
	// silently extend what a game-data:read token can reach.
	if _, err := harukiUtils.ParseUploadDataType("profile"); err == nil {
		t.Fatalf("OAuth2 game-data must not accept profile as a data type")
	}
	if postgresql.IsGrantableGameAccountDataType("recommend-data") {
		t.Fatalf("recommend-data is a derived capability, not a grantable type")
	}
}

func TestOAuth2MysekaiReadsUpdatedResourcesFromPostgres(t *testing.T) {
	dsn := os.Getenv("GAMEDATA_OAUTH_TEST_PG")
	if dsn == "" {
		t.Skip("set GAMEDATA_OAUTH_TEST_PG to a disposable database")
	}
	ctx := t.Context()
	pool, err := gamedata.NewPool(ctx, gamedata.PoolConfig{URL: dsn, MaxConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := gamedata.EnsureSchema(ctx, pool, true, catalog.Mysekai()); err != nil {
		t.Fatal(err)
	}
	env := newDeviceTestEnv(t)
	env.db.GameData = gamedata.NewService(pool)
	env.apiHelper.RuntimeConfig = runtimeconfig.New(runtimeconfig.Snapshot{AllowedKeys: []string{"upload_time"}}, nil)
	for _, userID := range []string{"owner", "grantee"} {
		env.db.DB.User.Create().SetID(userID).SetName(userID).SetEmail(userID + "@example.test").ExecX(ctx)
	}
	const gameID = int64(123)
	const strGameID = "123"
	env.db.DB.GameAccountBinding.Create().SetUserID("owner").SetServer("jp").SetGameUserID(strGameID).SetVerified(true).ExecX(ctx)
	if _, err := env.db.DB.UpsertGameAccountDataGrant(ctx, "owner", "grantee", "jp", strGameID, "mysekai", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-time.Minute).Unix()
	fixture := map[string]any{
		"upload_time": stamp,
		"updatedResources": map[string]any{
			"userMysekaiItems": []int{1, 2},
			"userMysekaiGates": []int{3},
		},
		"fixtureUnlistedField": true,
	}
	if _, err := env.db.GameData.Mysekai().Write(ctx, gameID, "jp", fixture, gamedata.WriteMysekai, env.db.GameData.Limits()); err != nil {
		t.Fatal(err)
	}

	env.app.Get("/:actor/game/:server/:data_type/:user_id", func(c fiber.Ctx) error {
		c.Locals("userID", c.Params("actor"))
		return c.Next()
	}, handleOAuth2GetGameData(env.apiHelper))
	get := func(actor, query, etag string, want int) (map[string]jsontext.Value, http.Header) {
		t.Helper()
		path := "/" + actor + "/game/jp/mysekai/" + strGameID + query
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if etag != "" {
			req.Header.Set(fiber.HeaderIfNoneMatch, etag)
		}
		resp, err := env.app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != want {
			t.Fatalf("GET %s = %d %s, %v, want %d", path, resp.StatusCode, body, err, want)
		}
		var result map[string]jsontext.Value
		if want == http.StatusOK && json.Unmarshal(body, &result) != nil {
			t.Fatalf("GET %s did not return a JSON object: %s", path, body)
		}
		if want == http.StatusNotModified && (len(body) != 0 || resp.Header.Get(data.HeaderUploadTime) != strconv.FormatInt(stamp, 10)) {
			t.Fatalf("invalid conditional response: %s %s", resp.Header.Get(data.HeaderUploadTime), body)
		}
		return result, resp.Header
	}
	for _, query := range []string{"?key=updatedResources", ""} {
		body, _ := get("owner", query, "", http.StatusOK)
		var resources map[string]jsontext.Value
		if err := json.Unmarshal(body["updatedResources"], &resources); err != nil || len(resources) != 2 ||
			string(resources["userMysekaiItems"]) != "[1,2]" || string(resources["userMysekaiGates"]) != "[3]" {
			t.Fatalf("GET %s returned unexpected nested resources: %s (%v)", query, body["updatedResources"], err)
		}
		if body["fixtureUnlistedField"] != nil || query != "" && len(body) != 1 || query == "" && string(body["upload_time"]) != strconv.FormatInt(stamp, 10) {
			t.Fatalf("GET %s returned unexpected projection: %v", query, body)
		}
	}
	get("owner", "?key=fixtureUnlistedField", "", http.StatusBadRequest)
	query := "?key=updatedResources,upload_time&known_upload_time=" + strconv.FormatInt(stamp, 10)
	_, header := get("grantee", query, "", http.StatusOK)
	etag := header.Get(fiber.HeaderETag)
	if etag == "" {
		t.Fatal("response did not carry a validator for the OAuth2 projection")
	}
	get("grantee", query, etag, http.StatusNotModified)
	if _, err := env.db.DB.DeleteGameAccountDataGrant(ctx, "owner", "grantee", "jp", strGameID, "mysekai"); err != nil {
		t.Fatal(err)
	}
	get("grantee", query, etag, http.StatusNotFound)
}

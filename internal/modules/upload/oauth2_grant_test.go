package upload

import (
	"context"
	"fmt"
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	oauth "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/background"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/gofiber/fiber/v3"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProxyOAuthWriteGrant(t *testing.T) {
	db := enttest.Open(t, "sqlite3", uniqueUploadAuditSQLiteDSN(t, "oauth-write-grant"))
	defer db.Close()
	ctx := context.Background()
	for _, id := range []string{"owner", "delegate", "stranger"} {
		if _, err := db.User.Create().SetID(id).SetName(id).SetEmail(id + "@example.com").Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.GameAccountBinding.Create().SetUserID("owner").SetServer("jp").SetGameUserID("123").SetVerified(true).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertGameAccountDataGrant(ctx, "owner", "delegate", "jp", "123", "suite", time.Now().Add(time.Hour), []string{"write"}); err != nil {
		t.Fatal(err)
	}
	hydra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			return
		}
		token := r.Form.Get("token")
		scope := "game-data:write bindings:read"
		actor := token
		if token == "read" {
			scope = "game-data:read"
			actor = "owner"
		}
		if token == "inactive" {
			fmt.Fprint(w, `{"active":false}`)
			return
		}
		fmt.Fprintf(w, `{"active":true,"sub":%q,"client_id":"proxy","scope":%q,"token_use":"access_token"}`, actor, scope)
	}))
	defer hydra.Close()
	d := testUploadDependencies()
	d.HydraConfig = oauth.NewHydraConfig(oauth.HydraConfigOptions{AdminURL: hydra.URL, RequestTimeout: time.Second})
	d.OAuth2ClientActiveChecker = func(context.Context, string) (bool, error) { return true, nil }
	d.BackgroundTasks = background.InlineRunner{}
	app := fiber.New()
	helper := &api.HarukiToolboxRouterHelpers{Router: app, DBManager: &database.HarukiToolboxDBManager{DB: db}}
	registerHarukiProxyRoutes(helper, d)
	for _, prefix := range []string{"/harukiproxy", "/api/harukiproxy"} {
		for _, tc := range []struct {
			token  string
			status int
			code   string
		}{{"", 401, "invalid_token"}, {"inactive", 401, "invalid_token"}, {"read", 403, "insufficient_scope"}, {"stranger", 403, "upload_not_allowed"}, {"owner", 400, "invalid_upload_payload"}, {"delegate", 400, "invalid_upload_payload"}} {
			req := httptest.NewRequest("POST", prefix+"/v3/jp/123/suite/upload", strings.NewReader("invalid raw game payload"))
			req.Header.Set("User-Agent", "HarukiProxy/v3.0.0 (platform=Android)")
			req.Header.Set("X-Haruki-Toolbox-Secret", "old-secret")
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != tc.status || !strings.Contains(string(body), tc.code) {
				t.Fatalf("%s: %d %s", tc.token, resp.StatusCode, body)
			}
		}
	}
	rows, err := db.UploadLog.Query().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.ActorUserID != nil && *row.ActorUserID == "delegate" {
			found = true
			if row.GrantID == nil || row.AuthorizationSource == nil || *row.AuthorizationSource != "grant" || row.AuthMethod == nil || *row.AuthMethod != "oauth2" {
				t.Fatalf("missing attribution %+v", row)
			}
		}
	}
	if !found {
		t.Fatal("missing delegate audit")
	}
}

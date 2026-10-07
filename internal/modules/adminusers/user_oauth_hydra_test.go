package adminusers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"

	"encoding/json/jsontext"
	json "encoding/json/v2"
	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

func TestHydraTokenStatsMarkedInexact(t *testing.T) {
	hydraConfig := oauth2.NewHydraConfig(oauth2.HydraConfigOptions{Provider: oauth2.ProviderHydra})
	if !hydraConfig.Enabled() {
		t.Fatalf("expected hydra provider to be enabled")
	}

	stats := adminOAuthTokenStats{Exact: false}
	if stats.Exact {
		t.Fatalf("expected hydra token stats to be marked inexact")
	}
}

// TestAdminListUserOAuthAuthorizationsExposesFlowTypeAndLabel checks the admin
// read-only mirror of the user's authorization list.
func TestAdminListUserOAuthAuthorizationsExposesFlowTypeAndLabel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/admin/oauth2/auth/sessions/consent" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("subject") != "kratos-target" {
			_, _ = io.WriteString(w, "[]")
			return
		}
		_, _ = io.WriteString(w, `[
			{"consent_request_id":"crid-web","grant_scope":["user:read"],
			 "consent_request":{"request_url":"https://hydra.example.com/oauth2/auth?client_id=web-app","client":{"client_id":"web-app"}}},
			{"consent_request_id":"crid-device","grant_scope":["user:read"],
			 "consent_request":{"request_url":"https://hydra.example.com/oauth2/device/verify?client_id=haruki-client","client":{"client_id":"haruki-client","token_endpoint_auth_method":"none"}},
			 "context":{"haruki":{"flow":"device","label":"Haruki-Client @ nas"}}}
		]`)
	}))
	t.Cleanup(server.Close)
	hydraConfig := oauth2.NewHydraConfig(oauth2.HydraConfigOptions{Provider: oauth2.ProviderHydra, AdminURL: server.URL, RequestTimeout: 5 * time.Second})

	client := enttest.Open(t, "sqlite3", "file:admin-user-oauth-flow-type-test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: client}}
	if _, err := client.User.Create().SetID("target-1").SetName("Target").SetEmail("target@example.com").SetRole(userSchema.RoleUser).SetKratosIdentityID("kratos-target").Save(t.Context()); err != nil {
		t.Fatal(err)
	}

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", "admin-1")
		c.Locals("userRole", "super_admin")
		return c.Next()
	})
	app.Get("/users/:target_user_id/oauth-authorizations", handleListUserOAuthAuthorizations(helper, hydraConfig))
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/users/target-1/oauth-authorizations", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.StatusCode, raw)
	}
	var envelope struct {
		UpdatedData struct {
			Items []map[string]jsontext.Value `json:"items"`
		} `json:"updatedData"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{"crid-web": {`"browser"`, `""`}, "crid-device": {`"device"`, `"Haruki-Client @ nas"`}}
	if len(envelope.UpdatedData.Items) != len(want) {
		t.Fatalf("items = %s", raw)
	}
	for _, item := range envelope.UpdatedData.Items {
		var crid string
		_ = json.Unmarshal(item["consentRequestId"], &crid)
		if got := [2]string{string(item["flowType"]), string(item["deviceLabel"])}; got != want[crid] {
			t.Errorf("%s: flowType, deviceLabel = %v, want %v", crid, got, want[crid])
		}
	}
}

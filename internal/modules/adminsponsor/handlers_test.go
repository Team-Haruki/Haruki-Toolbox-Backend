package adminsponsor

import (
	stdjson "encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sharedSponsor "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/sponsor"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	sponsorSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsor"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v3"
)

const testUserHeader = "X-Test-User"

var testDBSeq atomic.Int64

type adminTestEnv struct {
	app *fiber.App
	db  *postgresql.Client
}

func newAdminTestEnv(t *testing.T, afdian sharedSponsor.AfdianConfig) *adminTestEnv {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s_%d?mode=memory&cache=shared&_fk=1", name, testDBSeq.Add(1)))
	t.Cleanup(func() { _ = db.Close() })
	for _, u := range []struct {
		id   string
		role userSchema.Role
	}{{"admin-1", userSchema.RoleAdmin}, {"super-1", userSchema.RoleSuperAdmin}, {"user-1", userSchema.RoleUser}} {
		if _, err := db.User.Create().SetID(u.id).SetName(u.id).SetEmail(u.id + "@example.com").SetRole(u.role).Save(t.Context()); err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}
	app := fiber.New()
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{Router: app, DBManager: &database.HarukiToolboxDBManager{DB: db}}
	// Stands in for the session middleware of the real admin group.
	adminGroup := app.Group("/api/admin", func(c fiber.Ctx) error {
		if userID := c.Get(testUserHeader); userID != "" {
			c.Locals("userID", userID)
		}
		return c.Next()
	})
	RegisterAdminSponsorRoutes(helper, adminGroup, afdian)
	return &adminTestEnv{app: app, db: db}
}

type testResponse struct {
	Status int
	Body   []byte
}

func (e *adminTestEnv) do(t *testing.T, method, path, user, body string) testResponse {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if user != "" {
		req.Header.Set(testUserHeader, user)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.app.Test(req, fiber.TestConfig{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	return testResponse{Status: resp.StatusCode, Body: data}
}

type envelope[T any] struct {
	UpdatedData *T `json:"updatedData"`
}

func decode[T any](t *testing.T, resp testResponse) T {
	t.Helper()
	var out envelope[T]
	if err := stdjson.Unmarshal(resp.Body, &out); err != nil || out.UpdatedData == nil {
		t.Fatalf("decode %s: %v", resp.Body, err)
	}
	return *out.UpdatedData
}

func expectStatus(t *testing.T, resp testResponse, want int) {
	t.Helper()
	if resp.Status != want {
		t.Fatalf("status = %d, want %d: %s", resp.Status, want, resp.Body)
	}
}

func TestAdminSponsorManualDurationFlow(t *testing.T) {
	env := newAdminTestEnv(t, sharedSponsor.NewAfdianConfig(sharedSponsor.AfdianConfigOptions{}))

	expectStatus(t, env.do(t, http.MethodGet, "/api/admin/sponsors", "", ""), fiber.StatusUnauthorized)
	expectStatus(t, env.do(t, http.MethodGet, "/api/admin/sponsors", "user-1", ""), fiber.StatusForbidden)
	expectStatus(t, env.do(t, http.MethodPost, "/api/admin/sponsors", "admin-1", `{"name":"  "}`), fiber.StatusBadRequest)
	expectStatus(t, env.do(t, http.MethodPost, "/api/admin/sponsors", "admin-1", `{"name":"x","avatar":"`+strings.Repeat("a", 501)+`"}`), fiber.StatusBadRequest)
	expectStatus(t, env.do(t, http.MethodPost, "/api/admin/sponsors", "admin-1", `not json`), fiber.StatusBadRequest)

	created := decode[adminSponsorDetailResponse](t, env.do(t, http.MethodPost, "/api/admin/sponsors", "admin-1", `{"name":"线下赞助者","planName":"感谢"}`))
	id := created.Sponsor.ID
	if created.Sponsor.Category != string(sharedSponsor.CategoryFormer) || created.Sponsor.Source != "manual" {
		t.Fatalf("created sponsor = %+v", created.Sponsor)
	}
	base := "/api/admin/sponsors/" + id

	expectStatus(t, env.do(t, http.MethodPost, base+"/manual-durations", "admin-1", `{"amount":0,"unit":"day","note":"x"}`), fiber.StatusBadRequest)
	expectStatus(t, env.do(t, http.MethodPost, base+"/manual-durations", "admin-1", `{"amount":1,"unit":"year","note":"x"}`), fiber.StatusBadRequest)
	expectStatus(t, env.do(t, http.MethodPost, base+"/manual-durations", "admin-1", `{"amount":1,"unit":"day","note":"x","startsAt":"tomorrow"}`), fiber.StatusBadRequest)
	expectStatus(t, env.do(t, http.MethodPost, base+"/manual-durations", "admin-1", `{"amount":1,"unit":"day","note":"x","startsAt":"2099-01-01T00:00:00Z"}`), fiber.StatusBadRequest)
	expectStatus(t, env.do(t, http.MethodPost, "/api/admin/sponsors/missing/manual-durations", "admin-1", `{"amount":1,"unit":"day","note":"x"}`), fiber.StatusNotFound)

	added := decode[adminSponsorDetailResponse](t, env.do(t, http.MethodPost, base+"/manual-durations", "admin-1", `{"amount":2,"unit":"month","note":"微信转账"}`))
	if added.Sponsor.Category != string(sharedSponsor.CategoryCurrent) || len(added.ManualDurations) != 1 {
		t.Fatalf("after add: %+v", added)
	}
	entry := added.ManualDurations[0]
	if entry.CreatedBy != "admin-1" || entry.Origin != "admin" {
		t.Fatalf("entry = %+v", entry)
	}

	entryPath := fmt.Sprintf("%s/manual-durations/%d", base, entry.ID)
	updated := decode[adminSponsorDetailResponse](t, env.do(t, http.MethodPut, entryPath, "super-1", `{"amount":10,"unit":"day","startsAt":"2026-01-01T00:00:00Z","note":"改为 10 天"}`))
	if got := updated.ManualDurations[0]; got.Amount != 10 || got.UpdatedBy != "super-1" || got.Note != "改为 10 天" {
		t.Fatalf("after update: %+v", got)
	}
	expectStatus(t, env.do(t, http.MethodPut, base+"/manual-durations/abc", "admin-1", `{"amount":1}`), fiber.StatusBadRequest)
	expectStatus(t, env.do(t, http.MethodPut, base+"/manual-durations/999", "admin-1", `{"amount":1}`), fiber.StatusNotFound)
	expectStatus(t, env.do(t, http.MethodPut, entryPath, "admin-1", `{"note":""}`), fiber.StatusBadRequest)

	detail := decode[adminSponsorDetailResponse](t, env.do(t, http.MethodGet, base, "admin-1", ""))
	if detail.EffectiveExpiresAt == nil || detail.Sponsor.PlanExpiresAt == nil || !detail.EffectiveExpiresAt.Equal(*detail.Sponsor.PlanExpiresAt) {
		t.Fatalf("detail effective expiry = %v / %v", detail.EffectiveExpiresAt, detail.Sponsor.PlanExpiresAt)
	}
	expectStatus(t, env.do(t, http.MethodGet, "/api/admin/sponsors/missing", "admin-1", ""), fiber.StatusNotFound)

	list := decode[adminSponsorListResponse](t, env.do(t, http.MethodGet, "/api/admin/sponsors", "admin-1", ""))
	if list.Total != 1 || list.Items[0].ID != id {
		t.Fatalf("list = %+v", list)
	}

	expectStatus(t, env.do(t, http.MethodDelete, base+"/manual-durations/999", "admin-1", ""), fiber.StatusNotFound)
	afterDelete := decode[adminSponsorDetailResponse](t, env.do(t, http.MethodDelete, entryPath, "admin-1", ""))
	if len(afterDelete.ManualDurations) != 0 || afterDelete.Sponsor.Category != string(sharedSponsor.CategoryFormer) {
		t.Fatalf("after delete: %+v", afterDelete)
	}
}

func TestAdminSponsorUpdateIgnoresDerivedFields(t *testing.T) {
	env := newAdminTestEnv(t, sharedSponsor.NewAfdianConfig(sharedSponsor.AfdianConfigOptions{}))
	created := decode[adminSponsorDetailResponse](t, env.do(t, http.MethodPost, "/api/admin/sponsors", "admin-1", `{"name":"A"}`))
	path := "/api/admin/sponsors/" + created.Sponsor.ID

	resp := decode[adminSponsorMutationResponse](t, env.do(t, http.MethodPut, path, "admin-1", `{
		"name":"B","avatar":"https://example.com/a.png","planName":"档位","message":"hi","source":"legacy",
		"afdianSyncDisabled":true,"paidAt":"2026-01-01T00:00","isActive":true,"planExpiresAt":"2099-01-01T00:00:00Z"}`))
	got := resp.Sponsor
	if got.Name != "B" || got.Source != "legacy" || !got.AfdianSyncDisabled || got.PaidAt == nil {
		t.Fatalf("profile not saved: %+v", got)
	}
	if got.IsActive || got.PlanExpiresAt != nil || got.Category != string(sharedSponsor.CategoryFormer) {
		t.Fatalf("derived fields were taken from the request: %+v", got)
	}

	cleared := decode[adminSponsorMutationResponse](t, env.do(t, http.MethodPut, path, "admin-1", `{"name":"","avatar":"","planName":"","message":"","paidAt":""}`)).Sponsor
	if cleared.Avatar != "" || cleared.Message != "" || cleared.PaidAt != nil {
		t.Fatalf("fields not cleared: %+v", cleared)
	}
	expectStatus(t, env.do(t, http.MethodPut, path, "admin-1", `{"source":"paypal"}`), fiber.StatusBadRequest)
	expectStatus(t, env.do(t, http.MethodPut, path, "admin-1", `{"paidAt":"yesterday"}`), fiber.StatusBadRequest)
	expectStatus(t, env.do(t, http.MethodPut, path, "admin-1", `{"name":"`+strings.Repeat("n", 129)+`"}`), fiber.StatusBadRequest)
	expectStatus(t, env.do(t, http.MethodPut, "/api/admin/sponsors/missing", "admin-1", `{"name":"x"}`), fiber.StatusNotFound)
}

func TestAdminManualDurationRefusedBeforeSplit(t *testing.T) {
	env := newAdminTestEnv(t, sharedSponsor.NewAfdianConfig(sharedSponsor.AfdianConfigOptions{}))
	if err := env.db.Sponsor.Create().SetID("afdian_legacy").SetSource(sponsorSchema.SourceAfdian).SetAfdianUserID("legacy").Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	resp := env.do(t, http.MethodPost, "/api/admin/sponsors/afdian_legacy/manual-durations", "admin-1", `{"amount":1,"unit":"day","note":"x"}`)
	expectStatus(t, resp, fiber.StatusConflict)
	detail := decode[adminSponsorDetailResponse](t, env.do(t, http.MethodGet, "/api/admin/sponsors/afdian_legacy", "admin-1", ""))
	if !detail.Sponsor.DurationMigrationPending {
		t.Fatalf("pending flag missing: %+v", detail.Sponsor)
	}
}

func TestAdminSyncAfdianRequiresSuperAdminAndCredentials(t *testing.T) {
	env := newAdminTestEnv(t, sharedSponsor.NewAfdianConfig(sharedSponsor.AfdianConfigOptions{}))
	expectStatus(t, env.do(t, http.MethodPost, "/api/admin/sponsors/sync/afdian", "admin-1", ""), fiber.StatusForbidden)
	expectStatus(t, env.do(t, http.MethodPost, "/api/admin/sponsors/sync/afdian", "super-1", ""), fiber.StatusBadRequest)

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer failing.Close()
	upstreamDown := newAdminTestEnv(t, sharedSponsor.NewAfdianConfig(sharedSponsor.AfdianConfigOptions{UserID: "dev", APIToken: "token", APIBaseURL: failing.URL}))
	resp := upstreamDown.do(t, http.MethodPost, "/api/admin/sponsors/sync/afdian", "super-1", "")
	expectStatus(t, resp, fiber.StatusBadGateway)
	if strings.Contains(string(resp.Body), failing.URL) || strings.Contains(string(resp.Body), "status 502") {
		t.Fatalf("upstream details leaked: %s", resp.Body)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ec":200,"data":{"total_page":1,"list":[]}}`))
	}))
	defer server.Close()
	configured := newAdminTestEnv(t, sharedSponsor.NewAfdianConfig(sharedSponsor.AfdianConfigOptions{UserID: "dev", APIToken: "token", APIBaseURL: server.URL}))
	result := decode[sharedSponsor.AfdianSyncResult](t, configured.do(t, http.MethodPost, "/api/admin/sponsors/sync/afdian", "super-1", ""))
	if !result.Full {
		t.Fatalf("admin sync should walk every order page: %+v", result)
	}
}

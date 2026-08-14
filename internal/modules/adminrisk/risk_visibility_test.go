package adminrisk

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/riskevent"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v3"
	_ "github.com/mattn/go-sqlite3"
)

func newAdminRiskTestHelper(t *testing.T, databaseName string) *harukiAPIHelper.HarukiToolboxRouterHelpers {
	t.Helper()

	client := enttest.Open(t, "sqlite3", "file:"+databaseName+"?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	return &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{DB: client},
	}
}

func seedAdminRiskUser(t *testing.T, db *postgresql.Client, id string, role userSchema.Role) {
	t.Helper()
	if _, err := db.User.Create().
		SetID(id).
		SetName(id).
		SetEmail(id + "@example.com").
		SetRole(role).
		Save(t.Context()); err != nil {
		t.Fatalf("seed user %s: %v", id, err)
	}
}

func newAdminRiskTestApp(actorUserID, actorRole string) *fiber.App {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", actorUserID)
		c.Locals("userRole", actorRole)
		return c.Next()
	})
	return app
}

func createAdminRiskTestEvent(t *testing.T, db *postgresql.Client, eventTime time.Time, actorUserID, targetUserID string) *postgresql.RiskEvent {
	t.Helper()
	builder := db.RiskEvent.Create().
		SetEventTime(eventTime).
		SetStatus(riskevent.StatusOpen).
		SetSeverity(riskevent.SeverityMedium).
		SetSource("test")
	if actorUserID != "" {
		builder.SetActorUserID(actorUserID)
	}
	if targetUserID != "" {
		builder.SetTargetUserID(targetUserID)
	}
	row, err := builder.Save(t.Context())
	if err != nil {
		t.Fatalf("seed risk event: %v", err)
	}
	return row
}

func getRiskEventsResponse(t *testing.T, app *fiber.App, path string) (int, riskEventQueryResponse) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	var envelope struct {
		UpdatedData riskEventQueryResponse `json:"updatedData"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode GET %s response: %v", path, err)
	}
	return resp.StatusCode, envelope.UpdatedData
}

func TestHandleListRiskEventsScopesCountAndPaginationByAdminRole(t *testing.T) {
	helper := newAdminRiskTestHelper(t, "admin-risk-visibility")
	db := helper.DBManager.DB
	seedAdminRiskUser(t, db, "admin-1", userSchema.RoleAdmin)
	seedAdminRiskUser(t, db, "user-1", userSchema.RoleUser)
	seedAdminRiskUser(t, db, "super-1", userSchema.RoleSuperAdmin)

	eventTime := time.Now().UTC().Truncate(time.Second)
	visibleFirst := createAdminRiskTestEvent(t, db, eventTime, "", "")
	createAdminRiskTestEvent(t, db, eventTime, "super-1", "user-1")
	visibleSecond := createAdminRiskTestEvent(t, db, eventTime, "user-1", "")
	createAdminRiskTestEvent(t, db, eventTime, "user-1", "super-1")
	visibleThird := createAdminRiskTestEvent(t, db, eventTime, "external-actor", "deleted-target")

	query := url.Values{}
	query.Set("from", eventTime.Add(-time.Minute).Format(time.RFC3339))
	query.Set("to", eventTime.Add(time.Minute).Format(time.RFC3339))
	query.Set("sort", riskEventSortIDAsc)
	query.Set("page_size", "2")

	adminApp := newAdminRiskTestApp("admin-1", "admin")
	adminApp.Get("/events", handleListRiskEvents(helper))
	status, firstPage := getRiskEventsResponse(t, adminApp, "/events?"+query.Encode())
	if status != fiber.StatusOK {
		t.Fatalf("admin list status = %d, want %d", status, fiber.StatusOK)
	}
	if firstPage.Total != 3 || firstPage.TotalPages != 2 || !firstPage.HasMore {
		t.Fatalf("admin pagination = total %d, pages %d, hasMore %v; want 3, 2, true", firstPage.Total, firstPage.TotalPages, firstPage.HasMore)
	}
	if len(firstPage.Items) != 2 || firstPage.Items[0].ID != visibleFirst.ID || firstPage.Items[1].ID != visibleSecond.ID {
		t.Fatalf("admin first page IDs = %v, want [%d %d]", riskEventItemIDs(firstPage.Items), visibleFirst.ID, visibleSecond.ID)
	}

	query.Set("page", "2")
	_, secondPage := getRiskEventsResponse(t, adminApp, "/events?"+query.Encode())
	if secondPage.Total != 3 || secondPage.HasMore || len(secondPage.Items) != 1 || secondPage.Items[0].ID != visibleThird.ID {
		t.Fatalf("admin second page = total %d, hasMore %v, IDs %v; want 3, false, [%d]", secondPage.Total, secondPage.HasMore, riskEventItemIDs(secondPage.Items), visibleThird.ID)
	}

	query.Set("page", "1")
	query.Set("page_size", "10")
	superAdminApp := newAdminRiskTestApp("super-1", "super_admin")
	superAdminApp.Get("/events", handleListRiskEvents(helper))
	_, superAdminPage := getRiskEventsResponse(t, superAdminApp, "/events?"+query.Encode())
	if superAdminPage.Total != 5 || len(superAdminPage.Items) != 5 {
		t.Fatalf("super admin list = total %d, items %d; want 5, 5", superAdminPage.Total, len(superAdminPage.Items))
	}
}

func riskEventItemIDs(items []riskEventItem) []int {
	ids := make([]int, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func postAdminRiskHandler(t *testing.T, app *fiber.App, path, body string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp.StatusCode
}

func TestRiskEventMutationsEnforceReferencedUserRoleBoundary(t *testing.T) {
	helper := newAdminRiskTestHelper(t, "admin-risk-mutations")
	db := helper.DBManager.DB
	seedAdminRiskUser(t, db, "admin-1", userSchema.RoleAdmin)
	seedAdminRiskUser(t, db, "user-1", userSchema.RoleUser)
	seedAdminRiskUser(t, db, "super-1", userSchema.RoleSuperAdmin)

	app := newAdminRiskTestApp("admin-1", "admin")
	app.Post("/events", handleCreateRiskEvent(helper))
	app.Post("/events/:event_id/resolve", handleResolveRiskEvent(helper))

	if status := postAdminRiskHandler(t, app, "/events", `{"actorUserId":"super-1"}`); status != fiber.StatusForbidden {
		t.Fatalf("create with super admin actor status = %d, want %d", status, fiber.StatusForbidden)
	}
	if status := postAdminRiskHandler(t, app, "/events", `{"targetUserId":"super-1"}`); status != fiber.StatusForbidden {
		t.Fatalf("create with super admin target status = %d, want %d", status, fiber.StatusForbidden)
	}
	if status := postAdminRiskHandler(t, app, "/events", `{"actorUserId":"admin-1"}`); status != fiber.StatusOK {
		t.Fatalf("create with current admin actor status = %d, want %d", status, fiber.StatusOK)
	}
	if status := postAdminRiskHandler(t, app, "/events", `{"targetUserId":"admin-1"}`); status != fiber.StatusBadRequest {
		t.Fatalf("create with current admin target status = %d, want %d", status, fiber.StatusBadRequest)
	}
	if status := postAdminRiskHandler(t, app, "/events", `{"targetUserId":"deleted-user"}`); status != fiber.StatusOK {
		t.Fatalf("create with missing historical target status = %d, want %d", status, fiber.StatusOK)
	}

	eventTime := time.Now().UTC()
	hiddenResolve := createAdminRiskTestEvent(t, db, eventTime, "user-1", "super-1")
	if status := postAdminRiskHandler(t, app, fmt.Sprintf("/events/%d/resolve", hiddenResolve.ID), `{}`); status != fiber.StatusForbidden {
		t.Fatalf("resolve event targeting super admin status = %d, want %d", status, fiber.StatusForbidden)
	}
	unchanged, err := db.RiskEvent.Get(t.Context(), hiddenResolve.ID)
	if err != nil {
		t.Fatalf("reload forbidden resolve event: %v", err)
	}
	if unchanged.Status != riskevent.StatusOpen || unchanged.ResolvedAt != nil || unchanged.ResolvedBy != nil {
		t.Fatalf("forbidden resolve mutated event: %#v", unchanged)
	}

	selfActorResolve := createAdminRiskTestEvent(t, db, eventTime, "admin-1", "")
	if status := postAdminRiskHandler(t, app, fmt.Sprintf("/events/%d/resolve", selfActorResolve.ID), `{}`); status != fiber.StatusOK {
		t.Fatalf("resolve event with current admin actor status = %d, want %d", status, fiber.StatusOK)
	}
	selfTargetResolve := createAdminRiskTestEvent(t, db, eventTime, "", "admin-1")
	if status := postAdminRiskHandler(t, app, fmt.Sprintf("/events/%d/resolve", selfTargetResolve.ID), `{}`); status != fiber.StatusBadRequest {
		t.Fatalf("resolve event targeting current admin status = %d, want %d", status, fiber.StatusBadRequest)
	}
}

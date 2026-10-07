package botsecurity

import (
	"context"
	stdjson "encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/neopg"
	neopgEnttest "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/neopg/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/botsecurityalert"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/systemlog"
	userSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/user"

	"github.com/gofiber/fiber/v3"
)

const testUserHeader = "X-Test-User"

type adminTestEnv struct {
	app    *fiber.App
	helper *harukiAPIHelper.HarukiToolboxRouterHelpers
	db     *postgresql.Client
	logs   *logBuffer
	now    time.Time
}

func newAdminTestEnv(t *testing.T, owners OwnerLookup) *adminTestEnv {
	t.Helper()
	helper := newTestHelper(t)
	db := helper.DBManager.DB
	for _, u := range []struct {
		id   string
		role userSchema.Role
	}{
		{"admin-1", userSchema.RoleAdmin},
		{"admin-2", userSchema.RoleAdmin},
		{"super-1", userSchema.RoleSuperAdmin},
		{"user-1", userSchema.RoleUser},
	} {
		if _, err := db.User.Create().SetID(u.id).SetName("Name " + u.id).SetEmail(u.id + "@example.com").SetRole(u.role).Save(t.Context()); err != nil {
			t.Fatalf("seed user %s: %v", u.id, err)
		}
	}

	app := fiber.New()
	helper.Router = app
	// Stands in for the session middleware of the real admin group.
	adminGroup := app.Group("/api/admin", func(c fiber.Ctx) error {
		if userID := c.Get(testUserHeader); userID != "" {
			c.Locals("userID", userID)
		}
		return c.Next()
	})
	logger, logs := newTestLogger()
	env := &adminTestEnv{app: app, helper: helper, db: db, logs: logs, now: testNow}
	RegisterAdminRoutes(helper, adminGroup, AdminRouteOptions{
		OwnerLookup: owners,
		Logger:      logger,
		Now:         func() time.Time { return env.now },
	})
	return env
}

func (e *adminTestEnv) do(t *testing.T, method, path, user, body string) testResponse {
	t.Helper()
	headers := map[string]string{}
	if user != "" {
		headers[testUserHeader] = user
	}
	if body != "" {
		headers["Content-Type"] = "application/json"
	}
	return doRequest(t, e.app, method, path, body, headers)
}

type envelope[T any] struct {
	Status      int    `json:"status"`
	Message     string `json:"message"`
	UpdatedData *T     `json:"updatedData"`
}

type testAlertItem struct {
	ID        int     `json:"id"`
	Kind      string  `json:"kind"`
	BotID     *string `json:"botId"`
	OwnerQQ   *string `json:"ownerQq"`
	SourceIP  string  `json:"sourceIp"`
	Status    string  `json:"status"`
	Note      string  `json:"note"`
	HandledBy *struct {
		UserID string `json:"userId"`
		Name   string `json:"name"`
	} `json:"handledBy"`
	HandledAt *time.Time `json:"handledAt"`
}

type testListResponse struct {
	Items    []testAlertItem `json:"items"`
	Total    int             `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"pageSize"`
}

func (e *adminTestEnv) list(t *testing.T, query url.Values) testListResponse {
	t.Helper()
	resp := e.do(t, http.MethodGet, "/api/admin/bot-security/alerts?"+query.Encode(), "admin-1", "")
	if resp.Status != fiber.StatusOK {
		t.Fatalf("list %s: status %d body %s", query.Encode(), resp.Status, resp.Body)
	}
	got := decodeJSON[envelope[testListResponse]](t, resp.Body)
	if got.UpdatedData == nil {
		t.Fatalf("list %s: no updatedData in %s", query.Encode(), resp.Body)
	}
	return *got.UpdatedData
}

func itemIDs(items []testAlertItem) []int {
	ids := make([]int, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func equalIDs(got []int, want ...int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// fakeOwners is an OwnerLookup over a fixed map, or failing with err.
type fakeOwners struct {
	owners map[int]int64
	err    error
	calls  int
	asked  [][]int
}

func (f *fakeOwners) OwnerQQs(_ context.Context, botIDs []int) (map[int]int64, error) {
	f.calls++
	f.asked = append(f.asked, append([]int(nil), botIDs...))
	if f.err != nil {
		return nil, f.err
	}
	out := map[int]int64{}
	for _, id := range botIDs {
		if owner, ok := f.owners[id]; ok {
			out[id] = owner
		}
	}
	return out, nil
}

func TestAdminListFiltersAndPagination(t *testing.T) {
	owners := &fakeOwners{owners: map[int]int64{30042042: 1234567890123}}
	env := newAdminTestEnv(t, owners)
	base := testNow.Add(-time.Hour)
	a1 := mustCreateAlert(t, env.db, "auth_failed", "30042042", "203.0.113.7", "cn06", base)
	a2 := mustCreateAlert(t, env.db, "replay_detected", "30042042", "203.0.113.7", "cn06", base.Add(time.Minute))
	a3 := mustCreateAlert(t, env.db, "auth_failed", "", "198.51.100.9", "cn01", base.Add(2*time.Minute))
	a4 := mustCreateAlert(t, env.db, "auth_failed", "777", "198.51.100.9", "cn01", base.Add(3*time.Minute))
	a5 := mustCreateAlert(t, env.db, "rate_limited", "30042042", "", "cn06", base.Add(-48*time.Hour))
	if _, err := env.db.BotSecurityAlert.UpdateOneID(a2.ID).SetStatus(botsecurityalert.StatusResolved).Save(t.Context()); err != nil {
		t.Fatal(err)
	}

	all := env.list(t, url.Values{})
	if all.Total != 5 || all.Page != 1 || all.PageSize != 20 || !equalIDs(itemIDs(all.Items), a4.ID, a3.ID, a2.ID, a1.ID, a5.ID) {
		t.Fatalf("all = total %d page %d size %d ids %v", all.Total, all.Page, all.PageSize, itemIDs(all.Items))
	}
	// One batched owner lookup per page, distinct numeric bot ids only.
	if owners.calls != 1 || len(owners.asked[0]) != 2 {
		t.Fatalf("owner lookups = %d %v, want one call for 2 bots", owners.calls, owners.asked)
	}
	byID := map[int]testAlertItem{}
	for _, item := range all.Items {
		byID[item.ID] = item
	}
	if got := byID[a1.ID]; got.OwnerQQ == nil || *got.OwnerQQ != "1234567890123" || got.BotID == nil || *got.BotID != "30042042" {
		t.Fatalf("a1 owner/bot = %v/%v", got.OwnerQQ, got.BotID)
	}
	if got := byID[a3.ID]; got.OwnerQQ != nil || got.BotID != nil || got.SourceIP != "198.51.100.9" {
		t.Fatalf("a3 (no bot) = %+v, want null botId and ownerQq", got)
	}
	if got := byID[a4.ID]; got.OwnerQQ != nil {
		t.Fatalf("a4 (unregistered bot) ownerQq = %v, want null", *got.OwnerQQ)
	}

	check := func(name string, query url.Values, want ...int) {
		t.Helper()
		got := env.list(t, query)
		if got.Total != len(want) || !equalIDs(itemIDs(got.Items), want...) {
			t.Fatalf("%s: total %d ids %v, want %v", name, got.Total, itemIDs(got.Items), want)
		}
	}
	check("status", url.Values{"status": {"open"}}, a4.ID, a3.ID, a1.ID, a5.ID)
	check("status resolved", url.Values{"status": {"resolved"}}, a2.ID)
	check("status ignored", url.Values{"status": {"ignored"}})
	check("kind", url.Values{"kind": {"auth_failed"}}, a4.ID, a3.ID, a1.ID)
	check("botId", url.Values{"botId": {"30042042"}}, a2.ID, a1.ID, a5.ID)
	check("bot_id", url.Values{"bot_id": {"777"}}, a4.ID)
	check("from", url.Values{"from": {base.Add(time.Minute).Format(time.RFC3339)}}, a4.ID, a3.ID, a2.ID)
	check("to", url.Values{"to": {base.Format(time.RFC3339)}}, a1.ID, a5.ID)
	check("from+to offset", url.Values{
		"from": {base.Add(time.Minute).In(time.FixedZone("", 8*3600)).Format(time.RFC3339)},
		"to":   {base.Add(2 * time.Minute).Format(time.RFC3339)},
	}, a3.ID, a2.ID)
	check("combined", url.Values{"status": {"open"}, "kind": {"auth_failed"}, "botId": {"30042042"}}, a1.ID)

	page2 := env.list(t, url.Values{"page": {"2"}, "pageSize": {"2"}})
	if page2.Total != 5 || page2.Page != 2 || page2.PageSize != 2 || !equalIDs(itemIDs(page2.Items), a2.ID, a1.ID) {
		t.Fatalf("page 2 = total %d page %d size %d ids %v", page2.Total, page2.Page, page2.PageSize, itemIDs(page2.Items))
	}
	page3 := env.list(t, url.Values{"page": {"3"}, "page_size": {"2"}})
	if !equalIDs(itemIDs(page3.Items), a5.ID) {
		t.Fatalf("page 3 ids %v", itemIDs(page3.Items))
	}
	beyond := env.list(t, url.Values{"page": {"9"}})
	if beyond.Total != 5 || beyond.Items == nil || len(beyond.Items) != 0 {
		t.Fatalf("beyond = total %d items %v, want 5 and []", beyond.Total, beyond.Items)
	}
	if max := env.list(t, url.Values{"pageSize": {"100"}}); max.PageSize != 100 {
		t.Fatalf("pageSize 100 = %d", max.PageSize)
	}

	for _, query := range []url.Values{
		{"status": {"closed"}},
		{"kind": {"Auth"}},
		{"botId": {strings.Repeat("1", 65)}},
		{"from": {"yesterday"}},
		{"to": {"2026-10-08"}},
		{"from": {testNow.Format(time.RFC3339)}, "to": {base.Format(time.RFC3339)}},
		{"page": {"0"}},
		{"pageSize": {"101"}},
		{"pageSize": {"x"}},
	} {
		resp := env.do(t, http.MethodGet, "/api/admin/bot-security/alerts?"+query.Encode(), "admin-1", "")
		if resp.Status != fiber.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", query.Encode(), resp.Status)
		}
	}
}

func TestAdminListItemShape(t *testing.T) {
	env := newAdminTestEnv(t, &fakeOwners{owners: map[int]int64{30042042: 10001}})
	row, err := env.db.BotSecurityAlert.Create().
		SetKind("auth_failed").SetBotID("30042042").SetSubject("30042042").SetSourceIP("203.0.113.7").
		SetBuildID("b1").SetClientVersion("3.2.1").SetReason("invalid credential").SetEnforced(true).
		SetCount(5).SetThreshold(5).SetWindowSeconds(600).SetNode("cn06").
		SetAlertTime(testNow.Add(-time.Minute)).SetReceivedAt(testNow.Add(-time.Minute + time.Second)).
		Save(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	resp := env.do(t, http.MethodGet, "/api/admin/bot-security/alerts", "admin-1", "")
	var raw struct {
		UpdatedData struct {
			Items []map[string]any `json:"items"`
		} `json:"updatedData"`
	}
	if err := stdjson.Unmarshal(resp.Body, &raw); err != nil || len(raw.UpdatedData.Items) != 1 {
		t.Fatalf("decode %s: %v", resp.Body, err)
	}
	item := raw.UpdatedData.Items[0]
	want := map[string]any{
		"id": float64(row.ID), "kind": "auth_failed", "botId": "30042042", "ownerQq": "10001",
		"sourceIp": "203.0.113.7", "buildId": "b1", "clientVersion": "3.2.1", "reason": "invalid credential",
		"enforced": true, "count": float64(5), "threshold": float64(5), "windowSeconds": float64(600),
		"node": "cn06", "alertTime": testNow.Add(-time.Minute).Format(time.RFC3339),
		"receivedAt": testNow.Add(-time.Minute + time.Second).Format(time.RFC3339), "status": "open", "note": "",
		"handledBy": nil, "handledAt": nil,
	}
	if len(item) != len(want) {
		t.Fatalf("item has %d fields, want %d: %v", len(item), len(want), item)
	}
	for key, value := range want {
		got, ok := item[key]
		if !ok || got != value {
			t.Fatalf("item[%s] = %#v (present %v), want %#v", key, got, ok, value)
		}
	}
}

func TestAdminListOwnerLookupUnavailable(t *testing.T) {
	failing := &fakeOwners{err: errors.New("dial tcp: connection refused")}
	env := newAdminTestEnv(t, failing)
	mustCreateAlert(t, env.db, "auth_failed", "30042042", "203.0.113.7", "cn06", testNow.Add(-time.Minute))
	mustCreateAlert(t, env.db, "auth_failed", "30042043", "203.0.113.7", "cn06", testNow.Add(-2*time.Minute))
	got := env.list(t, url.Values{})
	if got.Total != 2 || got.Items[0].OwnerQQ != nil || got.Items[1].OwnerQQ != nil {
		t.Fatalf("list with failing bot DB = %+v, want owners null", got)
	}
	if n := strings.Count(env.logs.String(), "event=owner_lookup_failed"); n != 1 || strings.Contains(env.logs.String(), "connection refused") {
		t.Fatalf("want one owner_lookup_failed warning without the error text, logs: %s", env.logs.String())
	}

	// Not configured: owners null, nothing logged.
	env = newAdminTestEnv(t, NewBotDBOwnerLookup(func() *neopg.Client { return nil }))
	mustCreateAlert(t, env.db, "auth_failed", "30042042", "203.0.113.7", "cn06", testNow.Add(-time.Minute))
	if got := env.list(t, url.Values{}); got.Items[0].OwnerQQ != nil {
		t.Fatalf("ownerQq = %v without a bot DB", *got.Items[0].OwnerQQ)
	}
	if strings.Contains(env.logs.String(), "owner_lookup_failed") {
		t.Fatalf("unconfigured bot DB logged a failure: %s", env.logs.String())
	}
}

func TestBotDBOwnerLookup(t *testing.T) {
	name := strings.NewReplacer("/", "_").Replace(t.Name())
	botDB := neopgEnttest.Open(t, "sqlite3", "file:"+name+"?mode=memory&cache=shared&_fk=1")
	for botID, owner := range map[int]int64{30042042: 1234567890123, 30042043: 42} {
		if _, err := botDB.User.Create().SetBotID(botID).SetOwnerUserID(owner).Save(t.Context()); err != nil {
			t.Fatalf("seed bot user: %v", err)
		}
	}
	lookup := NewBotDBOwnerLookup(func() *neopg.Client { return botDB })
	owners, err := lookup.OwnerQQs(t.Context(), []int{30042042, 30042043, 1})
	if err != nil || len(owners) != 2 || owners[30042042] != 1234567890123 || owners[30042043] != 42 {
		t.Fatalf("owners = %v err %v", owners, err)
	}
	if owners, err := lookup.OwnerQQs(t.Context(), nil); err != nil || len(owners) != 0 {
		t.Fatalf("empty lookup = %v err %v", owners, err)
	}

	env := newAdminTestEnv(t, lookup)
	mustCreateAlert(t, env.db, "auth_failed", "30042042", "203.0.113.7", "cn06", testNow.Add(-time.Minute))
	mustCreateAlert(t, env.db, "auth_failed", "not-a-number", "203.0.113.7", "cn06", testNow.Add(-2*time.Minute))
	got := env.list(t, url.Values{})
	if got.Items[0].OwnerQQ == nil || *got.Items[0].OwnerQQ != "1234567890123" || got.Items[1].OwnerQQ != nil {
		t.Fatalf("items = %+v", got.Items)
	}

	// The bot DB going away degrades to null owners, never a failed list.
	_ = botDB.Close()
	got = env.list(t, url.Values{})
	if got.Total != 2 || got.Items[0].OwnerQQ != nil {
		t.Fatalf("closed bot DB list = %+v", got)
	}
	if !strings.Contains(env.logs.String(), "event=owner_lookup_failed") {
		t.Fatalf("closed bot DB not logged: %s", env.logs.String())
	}

	if _, err := NewBotDBOwnerLookup(nil).OwnerQQs(t.Context(), []int{1}); !errors.Is(err, ErrBotDBNotConfigured) {
		t.Fatalf("nil getter err = %v", err)
	}
	if _, err := helperBotDBOwnerLookup(nil).OwnerQQs(t.Context(), []int{1}); !errors.Is(err, ErrBotDBNotConfigured) {
		t.Fatalf("nil helper err = %v", err)
	}
}

func (e *adminTestEnv) patch(t *testing.T, id int, user, body string) testResponse {
	t.Helper()
	return e.do(t, http.MethodPatch, "/api/admin/bot-security/alerts/"+strconv.Itoa(id), user, body)
}

func (e *adminTestEnv) auditRows(t *testing.T) []*postgresql.SystemLog {
	t.Helper()
	rows, err := e.db.SystemLog.Query().
		Where(systemlog.ActionEQ(adminAuditActionAlertUpdate)).
		Order(systemlog.ByID()).
		All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestAdminUpdateTransitions(t *testing.T) {
	env := newAdminTestEnv(t, &fakeOwners{owners: map[int]int64{30042042: 10001}})
	row := mustCreateAlert(t, env.db, "auth_failed", "30042042", "203.0.113.7", "cn06", testNow.Add(-time.Minute))

	update := func(user, body string) testAlertItem {
		t.Helper()
		resp := env.patch(t, row.ID, user, body)
		if resp.Status != fiber.StatusOK {
			t.Fatalf("PATCH %s: status %d body %s", body, resp.Status, resp.Body)
		}
		got := decodeJSON[envelope[testAlertItem]](t, resp.Body)
		if got.UpdatedData == nil || got.UpdatedData.ID != row.ID {
			t.Fatalf("PATCH %s: body %s", body, resp.Body)
		}
		return *got.UpdatedData
	}

	// open -> resolved records the handler and the note.
	env.now = testNow.Add(time.Minute)
	got := update("admin-1", `{"status":"resolved","note":"  revoked the credential  "}`)
	if got.Status != "resolved" || got.Note != "revoked the credential" || got.HandledBy == nil ||
		got.HandledBy.UserID != "admin-1" || got.HandledBy.Name != "Name admin-1" ||
		got.HandledAt == nil || !got.HandledAt.Equal(env.now) || got.OwnerQQ == nil || *got.OwnerQQ != "10001" {
		t.Fatalf("resolved = %+v", got)
	}

	// Same status, note omitted: nothing about the handler changes.
	env.now = testNow.Add(2 * time.Minute)
	got = update("admin-2", `{"status":"resolved"}`)
	if got.Note != "revoked the credential" || got.HandledBy.UserID != "admin-1" || !got.HandledAt.Equal(testNow.Add(time.Minute)) {
		t.Fatalf("same-status update = %+v, want handler and note kept", got)
	}

	// resolved -> ignored is a new decision by the new admin.
	got = update("admin-2", `{"status":"IGNORED","note":""}`)
	if got.Status != "ignored" || got.Note != "" || got.HandledBy.UserID != "admin-2" || !got.HandledAt.Equal(env.now) {
		t.Fatalf("ignored = %+v", got)
	}

	// Reopen clears the handler.
	got = update("super-1", `{"status":"open"}`)
	if got.Status != "open" || got.HandledBy != nil || got.HandledAt != nil {
		t.Fatalf("reopened = %+v", got)
	}
	stored, _ := env.db.BotSecurityAlert.Get(t.Context(), row.ID)
	if stored.HandledByUserID != nil || stored.HandledAt != nil || stored.Status != botsecurityalert.StatusOpen {
		t.Fatalf("stored after reopen = %+v", stored)
	}

	audits := env.auditRows(t)
	if len(audits) != 4 {
		t.Fatalf("audit rows = %d, want 4", len(audits))
	}
	first := audits[0]
	if first.Result != systemlog.ResultSuccess || first.TargetType == nil || *first.TargetType != "bot_security_alert" ||
		first.TargetID == nil || *first.TargetID != strconv.Itoa(row.ID) || first.ActorUserID == nil || *first.ActorUserID != "admin-1" ||
		first.Metadata["fromStatus"] != "open" || first.Metadata["toStatus"] != "resolved" || first.Metadata["noteChanged"] != true {
		t.Fatalf("first audit = %+v", first)
	}
	if audits[3].Metadata["fromStatus"] != "ignored" || audits[3].Metadata["toStatus"] != "open" || audits[3].Metadata["noteChanged"] != false {
		t.Fatalf("reopen audit = %+v", audits[3].Metadata)
	}
}

func TestAdminUpdateRejects(t *testing.T) {
	env := newAdminTestEnv(t, nil)
	row := mustCreateAlert(t, env.db, "auth_failed", "30042042", "203.0.113.7", "cn06", testNow.Add(-time.Minute))

	cases := []struct {
		name   string
		path   string
		body   string
		status int
	}{
		{"bad id", "/api/admin/bot-security/alerts/abc", `{"status":"resolved"}`, fiber.StatusBadRequest},
		{"zero id", "/api/admin/bot-security/alerts/0", `{"status":"resolved"}`, fiber.StatusBadRequest},
		{"missing status", "", `{"note":"x"}`, fiber.StatusBadRequest},
		{"unknown status", "", `{"status":"closed"}`, fiber.StatusBadRequest},
		{"not json", "", `status=resolved`, fiber.StatusBadRequest},
		{"long note", "", `{"status":"resolved","note":"` + strings.Repeat("注", 1001) + `"}`, fiber.StatusBadRequest},
		{"unknown alert", "/api/admin/bot-security/alerts/999999", `{"status":"resolved"}`, fiber.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.path
			if path == "" {
				path = "/api/admin/bot-security/alerts/" + strconv.Itoa(row.ID)
			}
			resp := env.do(t, http.MethodPatch, path, "admin-1", tc.body)
			if resp.Status != tc.status {
				t.Fatalf("status %d body %s, want %d", resp.Status, resp.Body, tc.status)
			}
		})
	}
	// A note of exactly 1000 characters is fine.
	if resp := env.patch(t, row.ID, "admin-1", `{"status":"resolved","note":"`+strings.Repeat("注", 1000)+`"}`); resp.Status != fiber.StatusOK {
		t.Fatalf("1000-character note: status %d body %s", resp.Status, resp.Body)
	}
	for _, audit := range env.auditRows(t)[:len(env.auditRows(t))-1] {
		if audit.Result != systemlog.ResultFailure {
			t.Fatalf("rejected update audited as %s", audit.Result)
		}
	}
}

func TestAdminRoutesRequireAdmin(t *testing.T) {
	env := newAdminTestEnv(t, nil)
	row := mustCreateAlert(t, env.db, "auth_failed", "30042042", "203.0.113.7", "cn06", testNow.Add(-time.Minute))
	if _, err := env.db.User.UpdateOneID("admin-2").SetBanned(true).Save(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		user   string
		status int
	}{
		{"", fiber.StatusUnauthorized},
		{"ghost", fiber.StatusUnauthorized},
		{"user-1", fiber.StatusForbidden},
		{"admin-2", fiber.StatusForbidden},
	} {
		for _, req := range []struct{ method, path, body string }{
			{http.MethodGet, "/api/admin/bot-security/alerts", ""},
			{http.MethodGet, "/api/admin/bot-security/summary", ""},
			{http.MethodPatch, "/api/admin/bot-security/alerts/" + strconv.Itoa(row.ID), `{"status":"resolved"}`},
		} {
			if resp := env.do(t, req.method, req.path, tc.user, req.body); resp.Status != tc.status {
				t.Fatalf("%s %s as %q: status %d, want %d", req.method, req.path, tc.user, resp.Status, tc.status)
			}
		}
	}
	stored, _ := env.db.BotSecurityAlert.Get(t.Context(), row.ID)
	if stored.Status != botsecurityalert.StatusOpen {
		t.Fatalf("non-admin changed the alert to %s", stored.Status)
	}
	if len(env.auditRows(t)) != 0 {
		t.Fatal("non-admin request wrote an update audit entry")
	}
	for _, user := range []string{"admin-1", "super-1"} {
		if resp := env.do(t, http.MethodGet, "/api/admin/bot-security/summary", user, ""); resp.Status != fiber.StatusOK {
			t.Fatalf("%s summary: status %d", user, resp.Status)
		}
	}
}

func TestAdminSummary(t *testing.T) {
	env := newAdminTestEnv(t, nil)

	resp := env.do(t, http.MethodGet, "/api/admin/bot-security/summary", "admin-1", "")
	if resp.Status != fiber.StatusOK || !strings.Contains(string(resp.Body), `"byKind":[]`) {
		t.Fatalf("empty summary: status %d body %s, want byKind []", resp.Status, resp.Body)
	}

	mustCreateAlert(t, env.db, "auth_failed", "1", "", "cn06", testNow.Add(-time.Hour))
	mustCreateAlert(t, env.db, "auth_failed", "2", "", "cn06", testNow.Add(-2*time.Hour))
	mustCreateAlert(t, env.db, "replay_detected", "1", "", "cn06", testNow.Add(-3*24*time.Hour))
	mustCreateAlert(t, env.db, "build_rejected", "3", "", "cn06", testNow.Add(-30*24*time.Hour))
	resolved := mustCreateAlert(t, env.db, "build_rejected", "4", "", "cn06", testNow.Add(-time.Minute))
	if _, err := env.db.BotSecurityAlert.UpdateOneID(resolved.ID).SetStatus(botsecurityalert.StatusResolved).Save(t.Context()); err != nil {
		t.Fatal(err)
	}

	resp = env.do(t, http.MethodGet, "/api/admin/bot-security/summary", "admin-1", "")
	got := decodeJSON[envelope[alertSummaryResponse]](t, resp.Body).UpdatedData
	if got == nil || got.Open != 4 || got.Last24h != 3 || got.Last7d != 4 {
		t.Fatalf("summary = %+v", got)
	}
	want := []kindCount{{"auth_failed", 2}, {"build_rejected", 1}, {"replay_detected", 1}}
	if len(got.ByKind) != len(want) {
		t.Fatalf("byKind = %+v, want %+v", got.ByKind, want)
	}
	for i := range want {
		if got.ByKind[i] != want[i] {
			t.Fatalf("byKind = %+v, want %+v", got.ByKind, want)
		}
	}
}

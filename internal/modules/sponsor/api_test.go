package sponsor

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	sponsorSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsor"

	"github.com/gofiber/fiber/v3"
)

// afdianStub answers query-order with the given orders (also filtered by
// out_trade_no) and query-sponsor with an empty list.
func afdianStub(t *testing.T, orders []map[string]any, status int, ec int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.UnmarshalRead(r.Body, &body)
		var params map[string]any
		_ = json.Unmarshal([]byte(body["params"].(string)), &params)
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		list := orders
		if no, ok := params["out_trade_no"].(string); ok {
			list = nil
			for _, order := range orders {
				if order["out_trade_no"] == no {
					list = append(list, order)
				}
			}
		}
		if strings.HasSuffix(r.URL.Path, "/query-sponsor") {
			list = nil
		}
		out, _ := json.Marshal(map[string]any{"ec": ec, "data": map[string]any{"total_page": 1, "list": list}})
		_, _ = w.Write(out)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestVerifyAfdianOrder(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	orders := []map[string]any{orderJSON("u", "real", "plan", 0, 3, now)}

	if _, _, err := VerifyAfdianOrder(ctx, NewAfdianConfig(AfdianConfigOptions{}), "real", now); !errors.Is(err, ErrAfdianNotConfigured) {
		t.Fatalf("unconfigured: %v", err)
	}
	cfg := NewAfdianConfig(AfdianConfigOptions{UserID: "dev", APIToken: "token", APIBaseURL: afdianStub(t, orders, http.StatusOK, 200).URL})
	order, found, err := VerifyAfdianOrder(ctx, cfg, " real ", now)
	if err != nil || !found || order.Month != 3 || !order.PaidAt.Equal(now) {
		t.Fatalf("real order: %+v found=%v err=%v", order, found, err)
	}
	if _, found, err := VerifyAfdianOrder(ctx, cfg, "forged", now); err != nil || found {
		t.Fatalf("forged order: found=%v err=%v", found, err)
	}
	if _, found, err := VerifyAfdianOrder(ctx, cfg, "", now); err != nil || found {
		t.Fatalf("empty trade no: found=%v err=%v", found, err)
	}
	for _, stub := range []*httptest.Server{afdianStub(t, orders, http.StatusBadGateway, 200), afdianStub(t, orders, http.StatusOK, 400)} {
		bad := NewAfdianConfig(AfdianConfigOptions{UserID: "dev", APIToken: "token", APIBaseURL: stub.URL})
		if _, _, err := VerifyAfdianOrder(ctx, bad, "real", now); err == nil {
			t.Fatalf("API error not reported")
		}
		if _, err := SyncAfdianSponsors(ctx, openSponsorDB(t), bad, now, SyncOptions{Full: true}); err == nil {
			t.Fatalf("sync ignored an API error")
		}
	}
	if _, err := SyncAfdianSponsors(ctx, openSponsorDB(t), NewAfdianConfig(AfdianConfigOptions{}), now, SyncOptions{}); !errors.Is(err, ErrAfdianNotConfigured) {
		t.Fatalf("unconfigured sync: %v", err)
	}
}

func newSponsorRouteApp(t *testing.T, cfg AfdianConfig) (*fiber.App, *harukiAPIHelper.HarukiToolboxRouterHelpers) {
	t.Helper()
	app := fiber.New()
	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{Router: app, DBManager: &database.HarukiToolboxDBManager{DB: openSponsorDB(t)}}
	RegisterSponsorRoutes(helper, cfg)
	return app, helper
}

func postJSON(t *testing.T, app *fiber.App, path string, body any) int {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestWebhookRecordsVerifiedOrderAndWallShowsCategory(t *testing.T) {
	now := time.Now().UTC()
	orders := []map[string]any{orderJSON("hook-user", "hook-1", "", 0, 2, now)}
	cfg := NewAfdianConfig(AfdianConfigOptions{UserID: "dev", APIToken: "token", APIBaseURL: afdianStub(t, orders, http.StatusOK, 200).URL, WebhookSecret: "callback-secret-0123456789"})
	app, helper := newSponsorRouteApp(t, cfg)

	webhook := map[string]any{"ec": 200, "data": map[string]any{"type": "order", "order": map[string]any{"out_trade_no": "hook-1", "user_id": "hook-user", "status": 2, "month": 99}}}
	// Wrong secret, forged order and unparsable bodies are acknowledged but not stored.
	postJSON(t, app, "/api/sponsor/afdian/callback/wrong-secret", webhook)
	forged := map[string]any{"data": map[string]any{"order": map[string]any{"out_trade_no": "forged", "user_id": "x", "status": 2}}}
	postJSON(t, app, "/api/sponsor/afdian/callback/callback-secret-0123456789", forged)
	postJSON(t, app, "/api/sponsor/afdian/callback/callback-secret-0123456789", map[string]any{"data": map[string]any{}})
	if n, _ := helper.DBManager.DB.Sponsor.Query().Count(t.Context()); n != 0 {
		t.Fatalf("unverified webhooks stored %d sponsors", n)
	}

	if status := postJSON(t, app, "/api/sponsor/afdian/callback/callback-secret-0123456789", webhook); status != http.StatusOK {
		t.Fatalf("webhook status %d", status)
	}
	row, err := helper.DBManager.DB.Sponsor.Get(t.Context(), "afdian_hook-user")
	if err != nil {
		t.Fatal(err)
	}
	// The API copy (2 months) wins over the webhook body (99 months).
	if row.AfdianDurationMonths != 2 || SponsorCategory(row, now) != CategoryCurrent {
		t.Fatalf("stored sponsor: months=%d category=%s", row.AfdianDurationMonths, SponsorCategory(row, now))
	}

	req := httptest.NewRequest(http.MethodGet, "/api/misc/sponsors", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var page struct {
		UpdatedData SponsorPageResponse `json:"updatedData"`
	}
	if err := json.UnmarshalRead(resp.Body, &page); err != nil {
		t.Fatal(err)
	}
	if page.UpdatedData.Summary.ActiveCount != 1 || page.UpdatedData.Supporters[0].Category != CategoryCurrent {
		t.Fatalf("wall = %+v", page.UpdatedData)
	}
}

func TestWebhookWithoutAPICredentialsTrustsSecretOnly(t *testing.T) {
	now := time.Now().UTC()
	app, helper := newSponsorRouteApp(t, NewAfdianConfig(AfdianConfigOptions{WebhookSecret: "callback-secret-0123456789"}))
	webhook := map[string]any{"data": map[string]any{"order": orderJSON("body-user", "body-1", "plan", 0, 1, now)}}
	postJSON(t, app, "/api/sponsor/afdian/callback/callback-secret-0123456789", webhook)
	if _, err := helper.DBManager.DB.Sponsor.Get(t.Context(), "afdian_body-user"); err != nil {
		t.Fatalf("secret-only webhook not stored: %v", err)
	}
	// Neither gate configured: nothing is stored.
	closed, closedHelper := newSponsorRouteApp(t, NewAfdianConfig(AfdianConfigOptions{}))
	postJSON(t, closed, "/api/sponsor/afdian/callback", webhook)
	if n, _ := closedHelper.DBManager.DB.Sponsor.Query().Count(t.Context()); n != 0 {
		t.Fatalf("fail-closed webhook stored %d sponsors", n)
	}
}

func TestParseAfdianWebhookPayloadShapes(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	order := orderJSON("u", "o", "plan", 0, 1, now)
	for name, payload := range map[string]map[string]any{
		"top-level order": {"order": order},
		"order in data":   {"data": map[string]any{"order": order}},
	} {
		if _, ok := ParseAfdianWebhookPayload(payload, now); !ok {
			t.Fatalf("%s did not parse", name)
		}
	}
	if _, ok := ParseAfdianWebhookPayload(map[string]any{"data": "x"}, now); ok {
		t.Fatalf("payload without an order parsed")
	}
	noUser := orderJSON("", "o", "plan", 0, 1, now)
	if _, ok := parseAfdianOrder(noUser, now); ok {
		t.Fatalf("order without user id parsed")
	}
	if _, ok := parseAfdianSponsorItem(map[string]any{"user": map[string]any{}}); ok {
		t.Fatalf("sponsor without user id parsed")
	}
	// Sale-plan current_plan expiry is not taken as a duration report.
	sale, ok := parseAfdianSponsorItem(map[string]any{"user_id": "u", "current_plan": map[string]any{"name": "周边", "product_type": 1, "expire_time": float64(now.Unix())}, "create_time": float64(now.Unix())})
	if !ok || sale.ReportedExpiresAt != nil || sale.LastPaidAt == nil {
		t.Fatalf("sale plan item = %+v", sale)
	}
}

func TestProfileSyncRespectsPinAndKeepsNewerPayment(t *testing.T) {
	ctx := context.Background()
	db := openSponsorDB(t)
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	item := sponsorItem("p", map[string]any{"plan_id": "plan", "name": "支持一下", "price": "15.00", "product_type": 0}, now)
	if err := UpsertAfdianSponsorProfile(ctx, db, item, now); err != nil {
		t.Fatal(err)
	}
	row, _ := db.Sponsor.Get(ctx, "afdian_p")
	if row.PlanRank != 1500 || stringPtrValue(row.PlanName) != "支持一下" || row.DurationSplitAt == nil {
		t.Fatalf("created row = %+v", row)
	}
	if _, err := row.Update().SetAfdianSyncDisabled(true).SetPlanName("手动档位").Save(ctx); err != nil {
		t.Fatal(err)
	}
	older := sponsorItem("p", map[string]any{"plan_id": "plan2", "name": "强烈支持一下", "price": "30.00"}, now.Add(-time.Hour))
	if err := UpsertAfdianSponsorProfile(ctx, db, older, now); err != nil {
		t.Fatal(err)
	}
	row, _ = db.Sponsor.Get(ctx, "afdian_p")
	if stringPtrValue(row.PlanName) != "手动档位" || row.PlanRank != 1500 || !row.PaidAt.Equal(now) {
		t.Fatalf("pinned row changed: plan=%q rank=%d paid=%v", stringPtrValue(row.PlanName), row.PlanRank, row.PaidAt)
	}
}

func TestManualDurationValidation(t *testing.T) {
	ctx := context.Background()
	db := openSponsorDB(t)
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	row, err := CreateManualSponsor(ctx, db, ManualSponsorInput{Name: "M"}, now)
	if err != nil {
		t.Fatal(err)
	}
	amount, day, month, note := 1, "day", "month", "x"
	tooMany, farAway, tomorrow := 1201, now.AddDate(200, 0, 0), now.Add(24*time.Hour)
	cases := map[string]ManualDurationInput{
		"missing fields":     {Amount: &amount},
		"too many months":    {Amount: &tooMany, Unit: &month, Note: &note},
		"start out of range": {Amount: &amount, Unit: &day, Note: &note, StartsAt: &farAway},
		"future gap":         {Amount: &amount, Unit: &day, Note: &note, StartsAt: &tomorrow},
	}
	for name, input := range cases {
		if _, err := AddManualDuration(ctx, db, row.ID, input, "admin", now); !errors.Is(err, ErrInvalidManualDuration) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	entry, err := AddManualDuration(ctx, db, row.ID, ManualDurationInput{Amount: &amount, Unit: &day, Note: &note}, "admin", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateManualDuration(ctx, db, row.ID, entry.ID, ManualDurationInput{Amount: &tooMany, Unit: &month}, "admin", now); !errors.Is(err, ErrInvalidManualDuration) {
		t.Fatalf("update too many: %v", err)
	}
	if _, err := UpdateManualDuration(ctx, db, row.ID, entry.ID, ManualDurationInput{StartsAt: &farAway}, "admin", now); !errors.Is(err, ErrInvalidManualDuration) {
		t.Fatalf("update start: %v", err)
	}
	if _, err := UpdateManualDuration(ctx, db, row.ID, entry.ID, ManualDurationInput{StartsAt: &tomorrow}, "admin", now.Add(48*time.Hour)); err != nil {
		t.Fatalf("a start that is no longer in the future: %v", err)
	}
	if _, err := UpdateManualDuration(ctx, db, row.ID, 999, ManualDurationInput{}, "admin", now); !errors.Is(err, ErrManualDurationNotFound) {
		t.Fatalf("update missing entry: %v", err)
	}
	if _, err := UpdateManualDuration(ctx, db, "missing", entry.ID, ManualDurationInput{}, "admin", now); err == nil {
		t.Fatalf("update on missing sponsor succeeded")
	}
	other, _ := CreateManualSponsor(ctx, db, ManualSponsorInput{Name: "Other"}, now)
	if err := DeleteManualDuration(ctx, db, other.ID, entry.ID, now); !errors.Is(err, ErrManualDurationNotFound) {
		t.Fatalf("delete through another sponsor: %v", err)
	}
	if err := db.Sponsor.Create().SetID("legacy").SetSource(sponsorSchema.SourceLegacy).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := DeleteManualDuration(ctx, db, "legacy", entry.ID, now); !errors.Is(err, ErrSponsorNotSplit) {
		t.Fatalf("delete on unsplit sponsor: %v", err)
	}
	if _, err := UpdateManualDuration(ctx, db, "legacy", entry.ID, ManualDurationInput{}, "admin", now); !errors.Is(err, ErrSponsorNotSplit) {
		t.Fatalf("update on unsplit sponsor: %v", err)
	}
}

func TestSplitIsClaimedOnce(t *testing.T) {
	ctx := context.Background()
	db := openSponsorDB(t)
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	expires := now.AddDate(0, 1, 0)
	if err := db.Sponsor.Create().SetID("manual_x").SetSource(sponsorSchema.SourceManual).SetPlanExpiresAt(expires).SetPaidAt(now).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	entry := &ManualDurationFacts{Amount: 31, Unit: ManualUnitDay, StartsAt: now}
	claimed, err := applySplit(ctx, db, "manual_x", entry, now)
	if err != nil || !claimed {
		t.Fatalf("first claim: %v %v", claimed, err)
	}
	claimed, err = applySplit(ctx, db, "manual_x", entry, now)
	if err != nil || claimed {
		t.Fatalf("second claim: %v %v", claimed, err)
	}
	if n, _ := db.SponsorManualDuration.Query().Count(ctx); n != 1 {
		t.Fatalf("entries = %d, want 1", n)
	}
	if err := RecomputeAllSponsors(ctx, db, now); err != nil {
		t.Fatal(err)
	}
	row, _ := db.Sponsor.Get(ctx, "manual_x")
	if !row.PlanExpiresAt.Equal(now.Add(31*24*time.Hour)) || !row.IsActive {
		t.Fatalf("recomputed = %v active=%v", row.PlanExpiresAt, row.IsActive)
	}
	if _, err := RecomputeSponsor(ctx, db, "missing", now); err == nil {
		t.Fatalf("recompute of a missing sponsor succeeded")
	}
	report, err := SplitLegacySponsorDurations(ctx, db, now, SplitOptions{OrdersComplete: true})
	if err != nil || report.AlreadySplit != 1 || !strings.Contains(report.String(), "already_split=1") {
		t.Fatalf("report = %v err=%v", report, err)
	}
}

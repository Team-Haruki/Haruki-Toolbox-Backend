package sponsor

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	sponsorSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsor"

	_ "github.com/mattn/go-sqlite3"
)

func uniqueSponsorSQLiteDSN(t *testing.T) string {
	t.Helper()
	name := strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())
	return fmt.Sprintf("file:%s-%d?mode=memory&cache=shared&_fk=1", name, time.Now().UnixNano())
}

func TestParseAfdianWebhookPayloadUsesOrderUserID(t *testing.T) {
	now := time.Date(2026, time.June, 20, 12, 0, 0, 0, time.UTC)
	payload := map[string]any{
		"ec": float64(200),
		"data": map[string]any{
			"type": "order",
			"order": map[string]any{
				"out_trade_no": "202106232138371083454010626",
				"user_id":      "adf397fe8374811eaacee52540025c377",
				"plan_id":      "a45353328af911eb973052540025c377",
				"month":        float64(1),
				"total_amount": "5.00",
				"status":       float64(2),
				"remark":       "谢谢工具箱",
			},
		},
	}

	parsed, ok := ParseAfdianWebhookPayload(payload, now)
	if !ok {
		t.Fatalf("expected webhook payload to parse")
	}
	if parsed.ID != "afdian_adf397fe8374811eaacee52540025c377" {
		t.Fatalf("id = %q, want sponsor user id based id", parsed.ID)
	}
	if parsed.AfdianUserID != "adf397fe8374811eaacee52540025c377" {
		t.Fatalf("afdian user id = %q", parsed.AfdianUserID)
	}
	if parsed.PlanPayMonths == nil || *parsed.PlanPayMonths != 1 {
		t.Fatalf("plan months = %#v, want 1", parsed.PlanPayMonths)
	}
	if parsed.PlanExpiresAt == nil || !parsed.PlanExpiresAt.Equal(now.AddDate(0, 1, 0)) {
		t.Fatalf("expires at = %v, want %v", parsed.PlanExpiresAt, now.AddDate(0, 1, 0))
	}
	if parsed.Message != "谢谢工具箱" {
		t.Fatalf("message = %q", parsed.Message)
	}
}

func TestParseAfdianWebhookPayloadClassifiesCustomOrderAsOneTime(t *testing.T) {
	now := time.Date(2026, time.June, 20, 12, 0, 0, 0, time.UTC)
	payload := map[string]any{
		"data": map[string]any{
			"order": map[string]any{
				"out_trade_no": "one-time-order",
				"user_id":      "one-time-user",
				"month":        float64(1),
				"total_amount": "30.00",
				"status":       float64(2),
			},
		},
	}

	parsed, ok := ParseAfdianWebhookPayload(payload, now)
	if !ok {
		t.Fatalf("expected webhook payload to parse")
	}
	if parsed.PlanName != oneTimePlanName {
		t.Fatalf("plan name = %q, want %q", parsed.PlanName, oneTimePlanName)
	}
	if parsed.PlanPayMonths != nil {
		t.Fatalf("plan months = %#v, want nil for one-time sponsor", parsed.PlanPayMonths)
	}
	if parsed.PlanExpiresAt != nil {
		t.Fatalf("expires at = %v, want nil for one-time sponsor", parsed.PlanExpiresAt)
	}
}

func TestUpsertParsedSponsorIncrementsSupportCountForNewOrders(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", uniqueSponsorSQLiteDSN(t))
	defer client.Close()

	now := time.Date(2026, time.June, 20, 12, 0, 0, 0, time.UTC)
	first, ok := parseAfdianOrder(map[string]any{
		"out_trade_no": "order-1",
		"user_id":      "same-user",
		"plan_id":      "monthly-plan",
		"month":        float64(1),
		"total_amount": "5.00",
		"status":       float64(2),
		"create_time":  float64(now.Unix()),
	}, now)
	if !ok {
		t.Fatalf("expected first order to parse")
	}
	if _, err := UpsertParsedSponsor(ctx, client, first, now, true); err != nil {
		t.Fatalf("upsert first order: %v", err)
	}

	second, ok := parseAfdianOrder(map[string]any{
		"out_trade_no": "order-2",
		"user_id":      "same-user",
		"plan_id":      "monthly-plan",
		"month":        float64(1),
		"total_amount": "5.00",
		"status":       float64(2),
		"create_time":  float64(now.Add(24 * time.Hour).Unix()),
	}, now)
	if !ok {
		t.Fatalf("expected second order to parse")
	}
	row, err := UpsertParsedSponsor(ctx, client, second, now, true)
	if err != nil {
		t.Fatalf("upsert second order: %v", err)
	}
	if row.SupportCount != 2 {
		t.Fatalf("support count = %d, want 2", row.SupportCount)
	}
	if row.OutTradeNo == nil || *row.OutTradeNo != "order-2" {
		t.Fatalf("out trade no = %#v, want latest order", row.OutTradeNo)
	}
}

func TestUpsertParsedSponsorSkipsSyncDisabledRecords(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", uniqueSponsorSQLiteDSN(t))
	defer client.Close()

	now := time.Date(2026, time.June, 20, 12, 0, 0, 0, time.UTC)
	order, ok := parseAfdianOrder(map[string]any{
		"out_trade_no": "order-1",
		"user_id":      "pinned-user",
		"plan_id":      "monthly-plan",
		"month":        float64(1),
		"total_amount": "5.00",
		"status":       float64(2),
		"create_time":  float64(now.Unix()),
	}, now)
	if !ok {
		t.Fatalf("expected order to parse")
	}
	created, err := UpsertParsedSponsor(ctx, client, order, now, true)
	if err != nil {
		t.Fatalf("upsert order: %v", err)
	}

	// Admin pins the record and rewrites the display name.
	if _, err := created.Update().SetAfdianSyncDisabled(true).SetName("管理员手动名").SetIsActive(false).Save(ctx); err != nil {
		t.Fatalf("pin sponsor: %v", err)
	}

	// A later sync/webhook for the same user must not touch the pinned record,
	// even though its future expiry would otherwise derive is_active = true.
	order.Name = "爱发电同步名"
	future := now.AddDate(0, 6, 0)
	order.PlanExpiresAt = &future
	row, err := UpsertParsedSponsor(ctx, client, order, now, true)
	if err != nil {
		t.Fatalf("re-upsert pinned order: %v", err)
	}
	if row.Name == nil || *row.Name != "管理员手动名" {
		t.Fatalf("name = %#v, want manual name preserved", row.Name)
	}
	if row.IsActive {
		t.Fatalf("is_active = true, want manual value preserved")
	}
	if row.SupportCount != 1 {
		t.Fatalf("support count = %d, want 1 (no increment for pinned record)", row.SupportCount)
	}
}

func TestSponsorPageResponseHidesPaymentAmount(t *testing.T) {
	now := time.Date(2026, time.June, 20, 12, 0, 0, 0, time.UTC)
	amount := "30.00"
	row := &postgresql.Sponsor{
		ID:           "amount-leak",
		Name:         stringPointerOrNil("赞助者"),
		PlanName:     stringPointerOrNil("月度赞助"),
		Source:       sponsorSchema.SourceAfdian,
		IsActive:     true,
		PlanRank:     3000,
		TotalAmount:  &amount,
		SupportCount: 1,
	}

	resp := BuildSponsorPageResponse([]*postgresql.Sponsor{row}, now)
	encoded, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	var decoded struct {
		Supporters []map[string]jsontext.Value `json:"supporters"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(decoded.Supporters) != 1 {
		t.Fatalf("supporters = %d, want 1", len(decoded.Supporters))
	}
	for _, key := range []string{"totalAmount", "planPrice", "planRank", "rank"} {
		if _, ok := decoded.Supporters[0][key]; ok {
			t.Fatalf("public supporter leaks payment field %q: %s", key, encoded)
		}
	}

	var plan struct {
		Rank *int `json:"rank"`
	}
	if raw, ok := decoded.Supporters[0]["plan"]; ok {
		if err := json.Unmarshal(raw, &plan); err != nil {
			t.Fatalf("unmarshal plan: %v", err)
		}
		if plan.Rank != nil {
			t.Fatalf("nested plan still exposes rank: %s", encoded)
		}
	}
}

func TestSortSponsorItemsTierThenDuration(t *testing.T) {
	now := time.Date(2026, time.June, 20, 12, 0, 0, 0, time.UTC)
	month := 1
	soon := now.Add(60 * 24 * time.Hour)
	later := now.Add(300 * 24 * time.Hour)

	// Lower tier but longer duration must still rank below a higher tier.
	lowTierLongDuration := &postgresql.Sponsor{
		ID:            "low-long",
		PlanName:      stringPointerOrNil("简单支持一下"),
		Source:        sponsorSchema.SourceAfdian,
		IsActive:      true,
		PlanRank:      500,
		PlanPayMonths: &month,
		PlanExpiresAt: &later,
		SupportCount:  1,
	}
	highTierShortDuration := &postgresql.Sponsor{
		ID:            "high-short",
		PlanName:      stringPointerOrNil("强烈支持一下"),
		Source:        sponsorSchema.SourceAfdian,
		IsActive:      true,
		PlanRank:      3000,
		PlanPayMonths: &month,
		PlanExpiresAt: &soon,
		SupportCount:  1,
	}
	// Same tier as above: longer remaining duration wins the tiebreak.
	highTierLongDuration := &postgresql.Sponsor{
		ID:            "high-long",
		PlanName:      stringPointerOrNil("强烈支持一下"),
		Source:        sponsorSchema.SourceAfdian,
		IsActive:      true,
		PlanRank:      3000,
		PlanPayMonths: &month,
		PlanExpiresAt: &later,
		SupportCount:  1,
	}

	resp := BuildSponsorPageResponse(
		[]*postgresql.Sponsor{lowTierLongDuration, highTierShortDuration, highTierLongDuration},
		now,
	)

	got := []string{resp.Supporters[0].ID, resp.Supporters[1].ID, resp.Supporters[2].ID}
	want := []string{"high-long", "high-short", "low-long"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sort order = %v, want %v", got, want)
		}
	}
}

func TestBuildSponsorPageResponseExpiresDurationSponsors(t *testing.T) {
	now := time.Date(2026, time.June, 20, 12, 0, 0, 0, time.UTC)
	oneTime := &postgresql.Sponsor{
		ID:           "one-time",
		PlanName:     stringPointerOrNil(oneTimePlanName),
		Source:       sponsorSchema.SourceAfdian,
		IsActive:     true,
		PlanRank:     3000,
		SupportCount: 1,
	}
	expiredAt := now.Add(-24 * time.Hour)
	month := 1
	expired := &postgresql.Sponsor{
		ID:            "expired-duration",
		PlanName:      stringPointerOrNil("月度赞助"),
		Source:        sponsorSchema.SourceAfdian,
		IsActive:      true,
		PlanRank:      500,
		PlanPayMonths: &month,
		PlanExpiresAt: &expiredAt,
		SupportCount:  1,
	}

	resp := BuildSponsorPageResponse([]*postgresql.Sponsor{oneTime, expired}, now)
	if resp.Summary.OneTimeCount != 1 {
		t.Fatalf("one time count = %d, want 1", resp.Summary.OneTimeCount)
	}
	if resp.Summary.ActiveCount != 1 || resp.Summary.PastCount != 1 {
		t.Fatalf("active/past = %d/%d, want 1/1", resp.Summary.ActiveCount, resp.Summary.PastCount)
	}
	for _, item := range resp.Supporters {
		if item.ID == "expired-duration" && item.IsActive {
			t.Fatalf("expired duration sponsor should be inactive in response")
		}
	}
}

func paidAfdianOrder(t *testing.T, userID string, outTradeNo string, planID string, month int, paidAt time.Time) parsedAfdianSponsor {
	t.Helper()
	order := map[string]any{
		"out_trade_no": outTradeNo,
		"user_id":      userID,
		"month":        float64(month),
		"total_amount": "5.00",
		"status":       float64(afdianOrderStatusPaid),
		"create_time":  float64(paidAt.Unix()),
	}
	if planID != "" {
		order["plan_id"] = planID
	}
	parsed, ok := parseAfdianOrder(order, paidAt)
	if !ok {
		t.Fatalf("expected paid order %q to parse", outTradeNo)
	}
	return parsed
}

// querySponsorItem mimics one entry of Afdian's query-sponsor list. plan is the
// current_plan object; nil omits it entirely (a one-time supporter).
func querySponsorItem(t *testing.T, userID string, plan map[string]any, lastPay time.Time) parsedAfdianSponsor {
	t.Helper()
	item := map[string]any{
		"user":           map[string]any{"user_id": userID, "name": "供养者"},
		"all_sum_amount": "5.00",
		"last_pay_time":  float64(lastPay.Unix()),
		"first_pay_time": float64(lastPay.Unix()),
	}
	if plan != nil {
		item["current_plan"] = plan
	}
	parsed, ok := parseAfdianSponsorItem(item, lastPay)
	if !ok {
		t.Fatalf("expected query-sponsor item for %q to parse", userID)
	}
	return parsed
}

func TestParseAfdianOrderAcceptsOnlyPaidStatus(t *testing.T) {
	now := time.Date(2026, time.June, 20, 12, 0, 0, 0, time.UTC)
	order := func(status float64) map[string]any {
		return map[string]any{
			"out_trade_no": "order-status",
			"user_id":      "status-user",
			"plan_id":      "monthly-plan",
			"month":        float64(1),
			"total_amount": "5.00",
			"status":       status,
			"create_time":  float64(now.Unix()),
		}
	}

	for _, status := range []float64{0, 1, 3} {
		if _, ok := parseAfdianOrder(order(status), now); ok {
			t.Fatalf("status %v accepted, want only paid orders (status 2)", status)
		}
	}
	// A missing status must not default to "paid" either.
	unset := order(0)
	delete(unset, "status")
	if _, ok := parseAfdianOrder(unset, now); ok {
		t.Fatalf("order without status accepted, want rejected")
	}
	parsed, ok := parseAfdianOrder(order(2), now)
	if !ok {
		t.Fatalf("status 2 rejected, want accepted")
	}
	if parsed.OutTradeNo != "order-status" {
		t.Fatalf("out trade no = %q", parsed.OutTradeNo)
	}
}

func TestUpsertParsedSponsorDeactivatesExpiredPlanOnSync(t *testing.T) {
	cases := []struct {
		name string
		plan func(paidAt time.Time) map[string]any
	}{
		{
			// Production shape: once the plan lapses Afdian keeps a current_plan
			// object but drops expire_time, so the item alone looks "active".
			name: "expire_time omitted",
			plan: func(time.Time) map[string]any { return map[string]any{"name": "", "plan_id": "monthly-plan"} },
		},
		{
			name: "expire_time in the past",
			plan: func(paidAt time.Time) map[string]any {
				return map[string]any{"plan_id": "monthly-plan", "name": "月度赞助", "pay_month": float64(1), "expire_time": float64(paidAt.AddDate(0, 1, 0).Unix())}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := enttest.Open(t, "sqlite3", uniqueSponsorSQLiteDSN(t))
			defer client.Close()

			paidAt := time.Date(2026, time.June, 20, 12, 0, 0, 0, time.UTC)
			created, err := UpsertParsedSponsor(ctx, client, paidAfdianOrder(t, "lapsed-user", "order-1", "monthly-plan", 1, paidAt), paidAt, true)
			if err != nil {
				t.Fatalf("upsert order: %v", err)
			}
			if !created.IsActive {
				t.Fatalf("freshly paid monthly sponsor should be active")
			}

			syncAt := paidAt.AddDate(0, 2, 0)
			row, err := UpsertParsedSponsor(ctx, client, querySponsorItem(t, "lapsed-user", tc.plan(paidAt), paidAt), syncAt, false)
			if err != nil {
				t.Fatalf("sync upsert: %v", err)
			}
			if row.IsActive {
				t.Fatalf("is_active = true after the plan expired, want false")
			}
			if row.PlanExpiresAt == nil || !row.PlanExpiresAt.Equal(paidAt.AddDate(0, 1, 0)) {
				t.Fatalf("plan_expires_at = %v, want original expiry %v preserved", row.PlanExpiresAt, paidAt.AddDate(0, 1, 0))
			}
		})
	}
}

func TestUpsertParsedSponsorReactivatesRenewedPlan(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", uniqueSponsorSQLiteDSN(t))
	defer client.Close()

	paidAt := time.Date(2026, time.June, 20, 12, 0, 0, 0, time.UTC)
	if _, err := UpsertParsedSponsor(ctx, client, paidAfdianOrder(t, "renew-user", "order-1", "monthly-plan", 1, paidAt), paidAt, true); err != nil {
		t.Fatalf("upsert first order: %v", err)
	}

	lapsedAt := paidAt.AddDate(0, 2, 0)
	row, err := UpsertParsedSponsor(ctx, client, querySponsorItem(t, "renew-user", map[string]any{"name": ""}, paidAt), lapsedAt, false)
	if err != nil {
		t.Fatalf("sync after lapse: %v", err)
	}
	if row.IsActive {
		t.Fatalf("is_active = true after lapse, want false before renewal")
	}

	// Webhook for a fresh payment extends the expiry and must flip it back.
	row, err = UpsertParsedSponsor(ctx, client, paidAfdianOrder(t, "renew-user", "order-2", "monthly-plan", 1, lapsedAt), lapsedAt, true)
	if err != nil {
		t.Fatalf("upsert renewal order: %v", err)
	}
	if !row.IsActive {
		t.Fatalf("is_active = false after renewal, want true")
	}
	if row.PlanExpiresAt == nil || !row.PlanExpiresAt.Equal(lapsedAt.AddDate(0, 1, 0)) {
		t.Fatalf("plan_expires_at = %v, want %v", row.PlanExpiresAt, lapsedAt.AddDate(0, 1, 0))
	}
	if row.SupportCount != 2 {
		t.Fatalf("support count = %d, want 2", row.SupportCount)
	}

	// The next sync sees the renewed plan with a future expire_time and keeps it active.
	renewedPlan := map[string]any{"plan_id": "monthly-plan", "name": "月度赞助", "pay_month": float64(1), "expire_time": float64(lapsedAt.AddDate(0, 1, 0).Unix())}
	row, err = UpsertParsedSponsor(ctx, client, querySponsorItem(t, "renew-user", renewedPlan, lapsedAt), lapsedAt.Add(time.Hour), false)
	if err != nil {
		t.Fatalf("sync after renewal: %v", err)
	}
	if !row.IsActive {
		t.Fatalf("is_active = false on sync after renewal, want true")
	}
}

func TestUpsertParsedSponsorKeepsNoPlanSponsorActive(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", uniqueSponsorSQLiteDSN(t))
	defer client.Close()

	paidAt := time.Date(2026, time.June, 20, 12, 0, 0, 0, time.UTC)
	created, err := UpsertParsedSponsor(ctx, client, paidAfdianOrder(t, "one-time-user", "order-1", "", 1, paidAt), paidAt, true)
	if err != nil {
		t.Fatalf("upsert one-time order: %v", err)
	}
	if created.PlanExpiresAt != nil || created.PlanID != nil {
		t.Fatalf("one-time order stored plan %#v / expiry %v, want none", created.PlanID, created.PlanExpiresAt)
	}
	if !created.IsActive {
		t.Fatalf("one-time sponsor should be active on creation")
	}

	// A year of syncs without any plan must not demote a no-plan sponsor.
	row, err := UpsertParsedSponsor(ctx, client, querySponsorItem(t, "one-time-user", nil, paidAt), paidAt.AddDate(1, 0, 0), false)
	if err != nil {
		t.Fatalf("sync one-time sponsor: %v", err)
	}
	if !row.IsActive {
		t.Fatalf("is_active = false for a sponsor without a plan, want permanently active")
	}
	if row.PlanExpiresAt != nil {
		t.Fatalf("plan_expires_at = %v, want nil", row.PlanExpiresAt)
	}
}

func TestSyncAfdianSponsorsDeactivatesExpiredPlans(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", uniqueSponsorSQLiteDSN(t))
	defer client.Close()

	paidAt := time.Date(2026, time.June, 20, 12, 0, 0, 0, time.UTC)
	syncAt := paidAt.AddDate(0, 2, 0)
	for _, user := range []string{"lapsed-user", "renewed-user"} {
		if _, err := UpsertParsedSponsor(ctx, client, paidAfdianOrder(t, user, "order-"+user, "monthly-plan", 1, paidAt), paidAt, true); err != nil {
			t.Fatalf("seed %s: %v", user, err)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/query-sponsor") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"ec":200,"em":"","data":{"total_count":3,"total_page":1,"list":[
			{"user":{"user_id":"lapsed-user","name":"A"},"all_sum_amount":"5.00","last_pay_time":%d,"current_plan":{"name":""}},
			{"user":{"user_id":"renewed-user","name":"B"},"all_sum_amount":"10.00","last_pay_time":%d,"current_plan":{"plan_id":"monthly-plan","name":"月度赞助","pay_month":1,"expire_time":%d}},
			{"user":{"user_id":"one-time-user","name":"C"},"all_sum_amount":"30.00","last_pay_time":%d}
		]}}`, paidAt.Unix(), syncAt.Unix(), syncAt.AddDate(0, 1, 0).Unix(), paidAt.Unix())
	}))
	defer server.Close()

	cfg := NewAfdianConfig(AfdianConfigOptions{UserID: "dev", APIToken: "token", APIBaseURL: server.URL})
	result, err := SyncAfdianSponsors(ctx, client, cfg, syncAt)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if result.Imported != 3 || result.Skipped != 0 {
		t.Fatalf("sync result = %+v, want 3 imported", result)
	}

	want := map[string]bool{
		"afdian_lapsed-user":   false,
		"afdian_renewed-user":  true,
		"afdian_one-time-user": true,
	}
	for id, active := range want {
		row, err := client.Sponsor.Query().Where(sponsorSchema.IDEQ(id)).Only(ctx)
		if err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
		if row.IsActive != active {
			t.Fatalf("%s is_active = %v, want %v", id, row.IsActive, active)
		}
	}
}

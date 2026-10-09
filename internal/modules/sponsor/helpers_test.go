package sponsor

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent"

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

func openSponsorDB(t *testing.T) *postgresql.Client {
	t.Helper()
	client := enttest.Open(t, "sqlite3", uniqueSponsorSQLiteDSN(t))
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// orderJSON is a query-order / webhook order object.
func orderJSON(userID, outTradeNo, planID string, productType, month int, paidAt time.Time) map[string]any {
	return map[string]any{
		"out_trade_no": outTradeNo,
		"user_id":      userID,
		"user_name":    "供养者",
		"plan_id":      planID,
		"plan_title":   "",
		"product_type": float64(productType),
		"month":        float64(month),
		"total_amount": "5.00",
		"show_amount":  "5.00",
		"status":       float64(afdianOrderStatusPaid),
		"remark":       "",
		"create_time":  float64(paidAt.Unix()),
	}
}

func mustParseOrder(t *testing.T, raw map[string]any, now time.Time) parsedAfdianOrder {
	t.Helper()
	parsed, ok := parseAfdianOrder(raw, now)
	if !ok {
		t.Fatalf("order %v did not parse", raw["out_trade_no"])
	}
	return parsed
}

func recordOrder(t *testing.T, db *postgresql.Client, raw map[string]any, now time.Time) *postgresql.Sponsor {
	t.Helper()
	row, err := RecordAfdianOrder(context.Background(), db, mustParseOrder(t, raw, now), now)
	if err != nil {
		t.Fatalf("record order: %v", err)
	}
	return row
}

func sponsorItem(userID string, plan map[string]any, lastPay time.Time) parsedAfdianSponsor {
	item := map[string]any{
		"user":           map[string]any{"user_id": userID, "name": "供养者"},
		"all_sum_amount": "5.00",
		"last_pay_time":  float64(lastPay.Unix()),
		"first_pay_time": float64(lastPay.Unix()),
	}
	if plan != nil {
		item["current_plan"] = plan
	}
	parsed, _ := parseAfdianSponsorItem(item)
	return parsed
}

func TestParseAfdianWebhookPayloadReadsOrderFields(t *testing.T) {
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
				"product_type": float64(0),
			},
		},
	}
	parsed, ok := ParseAfdianWebhookPayload(payload, now)
	if !ok {
		t.Fatalf("expected webhook payload to parse")
	}
	if parsed.AfdianUserID != "adf397fe8374811eaacee52540025c377" || parsed.Month != 1 || parsed.Remark != "谢谢工具箱" {
		t.Fatalf("parsed = %+v", parsed)
	}
	// No create_time in the webhook body: counts from the receive time.
	if !parsed.PaidAt.Equal(now) {
		t.Fatalf("paid at = %v, want receive time %v", parsed.PaidAt, now)
	}
	if ClassifyAfdianOrder(parsed.facts()) != AfdianOrderDuration {
		t.Fatalf("regular monthly order should be a duration order")
	}
}

func TestParseAfdianOrderAcceptsOnlyPaidStatus(t *testing.T) {
	now := time.Date(2026, time.June, 20, 12, 0, 0, 0, time.UTC)
	for _, status := range []float64{0, 1, 3} {
		raw := orderJSON("u", "o", "plan", 0, 1, now)
		raw["status"] = status
		if _, ok := parseAfdianOrder(raw, now); ok {
			t.Fatalf("status %v accepted, want only paid orders (status 2)", status)
		}
	}
	unset := orderJSON("u", "o", "plan", 0, 1, now)
	delete(unset, "status")
	if _, ok := parseAfdianOrder(unset, now); ok {
		t.Fatalf("order without status accepted, want rejected")
	}
}

func TestRecordAfdianOrderStacksCustomPlanOrders(t *testing.T) {
	db := openSponsorDB(t)
	first := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	recordOrder(t, db, orderJSON("custom-user", "o1", "", 0, 3, first), first) // 自选方案, 3 months
	renewal := first.AddDate(0, 1, 0)
	row := recordOrder(t, db, orderJSON("custom-user", "o2", "", 0, 2, renewal), renewal)

	want := truncateToAfdianDay(first.Add(5 * 31 * 24 * time.Hour))
	if row.PlanExpiresAt == nil || !row.PlanExpiresAt.Equal(want) {
		t.Fatalf("effective expiry = %v, want %v", row.PlanExpiresAt, want)
	}
	if row.AfdianDurationMonths != 5 || row.SupportCount != 2 || !row.HasDuration {
		t.Fatalf("months=%d support=%d has_duration=%v", row.AfdianDurationMonths, row.SupportCount, row.HasDuration)
	}
	if got := SponsorCategory(row, renewal); got != CategoryCurrent {
		t.Fatalf("category = %s, want current", got)
	}
	// Replaying a webhook is harmless.
	again := recordOrder(t, db, orderJSON("custom-user", "o2", "", 0, 2, renewal), renewal)
	if again.SupportCount != 2 || !again.PlanExpiresAt.Equal(want) {
		t.Fatalf("replayed webhook changed the sponsor: %+v", again)
	}
}

func TestOneTimeOnlySponsorIsOneTime(t *testing.T) {
	db := openSponsorDB(t)
	paidAt := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	row := recordOrder(t, db, orderJSON("shop-user", "o1", "item", afdianProductTypeForSale, 1, paidAt), paidAt)
	if row.HasDuration || row.PlanExpiresAt != nil {
		t.Fatalf("sale order granted time: %+v", row)
	}
	if got := SponsorCategory(row, paidAt); got != CategoryOneTime {
		t.Fatalf("category = %s, want one_time", got)
	}
	if got := DisplayPlanName(row, CategoryOneTime); got != oneTimePlanName {
		t.Fatalf("plan name = %q", got)
	}
}

// Requirement 2's root cause: once a plan lapsed, query-sponsor returns
// current_plan {name: ""}, and the old upsert stored the fallback label
// "一次性赞助" as the plan name, which the wall read as one-time.
func TestLapsedPlanBecomesFormerNotOneTime(t *testing.T) {
	ctx := context.Background()
	db := openSponsorDB(t)
	paidAt := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	recordOrder(t, db, orderJSON("lapsed-user", "o1", "plan", 0, 1, paidAt), paidAt)
	plan := map[string]any{"plan_id": "plan", "name": "支持一下", "pay_month": float64(1), "product_type": float64(0), "expire_time": float64(truncateToAfdianDay(paidAt.Add(31 * 24 * time.Hour)).Unix())}
	if err := UpsertAfdianSponsorProfile(ctx, db, sponsorItem("lapsed-user", plan, paidAt), paidAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	later := paidAt.AddDate(0, 2, 0)
	if err := UpsertAfdianSponsorProfile(ctx, db, sponsorItem("lapsed-user", map[string]any{"name": ""}, paidAt), later); err != nil {
		t.Fatal(err)
	}
	row, err := RecomputeSponsor(ctx, db, "afdian_lapsed-user", later)
	if err != nil {
		t.Fatal(err)
	}
	if got := SponsorCategory(row, later); got != CategoryFormer {
		t.Fatalf("category = %s, want former", got)
	}
	if got := stringPtrValue(row.PlanName); got != "支持一下" {
		t.Fatalf("stored plan name = %q, want the last plan kept", got)
	}
	if row.AfdianReportedExpiresAt == nil {
		t.Fatalf("the last reported expiry must be kept after the plan lapses")
	}
	resp := BuildSponsorPageResponse([]*postgresql.Sponsor{row}, later)
	if resp.Summary.PastCount != 1 || resp.Summary.OneTimeCount != 0 || resp.Supporters[0].Category != CategoryFormer {
		t.Fatalf("summary = %+v category = %s", resp.Summary, resp.Supporters[0].Category)
	}
}

func TestDisplayPlanNameHidesLegacyOneTimeLabel(t *testing.T) {
	label := oneTimePlanName
	row := &postgresql.Sponsor{PlanName: &label}
	if got := DisplayPlanName(row, CategoryFormer); got != defaultSponsorPlanName {
		t.Fatalf("former sponsor shows %q", got)
	}
}

func TestPinnedProfileKeepsTextButGainsTime(t *testing.T) {
	ctx := context.Background()
	db := openSponsorDB(t)
	paidAt := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	created := recordOrder(t, db, orderJSON("pinned-user", "o1", "plan", 0, 1, paidAt), paidAt)
	if _, err := created.Update().SetAfdianSyncDisabled(true).SetName("管理员手动名").Save(ctx); err != nil {
		t.Fatal(err)
	}
	renewal := paidAt.AddDate(0, 1, 0)
	if err := UpsertAfdianSponsorProfile(ctx, db, sponsorItem("pinned-user", map[string]any{"plan_id": "plan", "name": "新档位", "product_type": float64(0)}, renewal), renewal); err != nil {
		t.Fatal(err)
	}
	row := recordOrder(t, db, orderJSON("pinned-user", "o2", "plan", 0, 1, renewal), renewal)
	if stringPtrValue(row.Name) != "管理员手动名" || stringPtrValue(row.PlanName) == "新档位" {
		t.Fatalf("pinned profile overwritten: name=%q plan=%q", stringPtrValue(row.Name), stringPtrValue(row.PlanName))
	}
	if row.AfdianDurationMonths != 2 {
		t.Fatalf("pinned sponsor did not gain the renewal: months=%d", row.AfdianDurationMonths)
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
	for _, key := range []string{"totalAmount", "planPrice", "planRank", "rank", "manualDurations", "note", "afdianExpiresAt"} {
		if _, ok := decoded.Supporters[0][key]; ok {
			t.Fatalf("public supporter leaks field %q: %s", key, encoded)
		}
	}
	if strings.Contains(string(encoded), `"rank"`) {
		t.Fatalf("nested plan still exposes rank: %s", encoded)
	}
}

func TestSortSponsorItemsTierThenDuration(t *testing.T) {
	now := time.Date(2026, time.June, 20, 12, 0, 0, 0, time.UTC)
	split := now
	soon := now.Add(60 * 24 * time.Hour)
	later := now.Add(300 * 24 * time.Hour)
	mk := func(id string, rank int, expires time.Time) *postgresql.Sponsor {
		return &postgresql.Sponsor{ID: id, Source: sponsorSchema.SourceAfdian, PlanRank: rank, PlanExpiresAt: &expires, HasDuration: true, DurationSplitAt: &split, SupportCount: 1}
	}
	resp := BuildSponsorPageResponse([]*postgresql.Sponsor{mk("low-long", 500, later), mk("high-short", 3000, soon), mk("high-long", 3000, later)}, now)
	got := []string{resp.Supporters[0].ID, resp.Supporters[1].ID, resp.Supporters[2].ID}
	want := []string{"high-long", "high-short", "low-long"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sort order = %v, want %v", got, want)
		}
	}
}

func TestSponsorPageSummaryIsMutuallyExclusive(t *testing.T) {
	now := time.Date(2026, time.June, 20, 12, 0, 0, 0, time.UTC)
	split := now
	future, past := now.Add(time.Hour), now.Add(-time.Hour)
	rows := []*postgresql.Sponsor{
		{ID: "current", HasDuration: true, PlanExpiresAt: &future, DurationSplitAt: &split},
		{ID: "former", HasDuration: true, PlanExpiresAt: &past, DurationSplitAt: &split},
		{ID: "exactly-now", HasDuration: true, PlanExpiresAt: &now, DurationSplitAt: &split},
		{ID: "one-time", DurationSplitAt: &split},
		// Legacy row awaiting the split: judged by its single expiry.
		{ID: "legacy", PlanExpiresAt: &past},
	}
	resp := BuildSponsorPageResponse(rows, now)
	s := resp.Summary
	if s.ActiveCount != 1 || s.PastCount != 3 || s.OneTimeCount != 1 || s.ActiveCount+s.PastCount+s.OneTimeCount != s.SupporterCount {
		t.Fatalf("summary = %+v", s)
	}
	for _, item := range resp.Supporters {
		if item.IsActive != (item.Category == CategoryCurrent) {
			t.Fatalf("%s: isActive %v disagrees with category %s", item.ID, item.IsActive, item.Category)
		}
	}
}

func TestManualDurationLifecycle(t *testing.T) {
	ctx := context.Background()
	db := openSponsorDB(t)
	paidAt := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	afdian := recordOrder(t, db, orderJSON("mixed-user", "o1", "plan", 0, 1, paidAt), paidAt)
	afdianEnd := *afdian.AfdianExpiresAt

	amount, unit, note := 10, "day", "微信转账"
	entry, err := AddManualDuration(ctx, db, afdian.ID, ManualDurationInput{Amount: &amount, Unit: &unit, Note: &note}, "admin-1", paidAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	row, _ := db.Sponsor.Get(ctx, afdian.ID)
	if !row.PlanExpiresAt.Equal(afdianEnd.Add(10*24*time.Hour)) || !row.AfdianExpiresAt.Equal(afdianEnd) {
		t.Fatalf("effective=%v afdian=%v, want manual stacked after %v", row.PlanExpiresAt, row.AfdianExpiresAt, afdianEnd)
	}
	if entry.CreatedBy != "admin-1" || entry.Note != note {
		t.Fatalf("entry audit fields = %+v", entry)
	}

	months := "month"
	one := 1
	if _, err := UpdateManualDuration(ctx, db, afdian.ID, entry.ID, ManualDurationInput{Amount: &one, Unit: &months}, "admin-2", paidAt.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	row, _ = db.Sponsor.Get(ctx, afdian.ID)
	if !row.PlanExpiresAt.Equal(afdianEnd.Add(31 * 24 * time.Hour)) {
		t.Fatalf("after edit effective=%v", row.PlanExpiresAt)
	}

	if err := DeleteManualDuration(ctx, db, afdian.ID, entry.ID, paidAt.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	row, _ = db.Sponsor.Get(ctx, afdian.ID)
	if !row.PlanExpiresAt.Equal(afdianEnd) {
		t.Fatalf("after delete effective=%v, want Afdian end", row.PlanExpiresAt)
	}

	zero := 0
	if _, err := AddManualDuration(ctx, db, afdian.ID, ManualDurationInput{Amount: &zero, Unit: &unit, Note: &note}, "admin-1", paidAt); err != ErrInvalidManualDuration {
		t.Fatalf("zero amount: err = %v", err)
	}
	blank := "  "
	if _, err := AddManualDuration(ctx, db, afdian.ID, ManualDurationInput{Amount: &amount, Unit: &unit, Note: &blank}, "admin-1", paidAt); err != ErrInvalidManualDuration {
		t.Fatalf("blank note: err = %v", err)
	}
	if err := DeleteManualDuration(ctx, db, "afdian_other", entry.ID, paidAt); err == nil {
		t.Fatalf("delete on another sponsor succeeded")
	}
}

func TestManualOnlySponsor(t *testing.T) {
	ctx := context.Background()
	db := openSponsorDB(t)
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	row, err := CreateManualSponsor(ctx, db, ManualSponsorInput{Name: "线下赞助者"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := SponsorCategory(row, now); got != CategoryOneTime {
		t.Fatalf("no time yet: %s", got)
	}
	amount, unit, note := 2, "month", "QQ 红包"
	if _, err := AddManualDuration(ctx, db, row.ID, ManualDurationInput{Amount: &amount, Unit: &unit, Note: &note}, "admin-1", now); err != nil {
		t.Fatal(err)
	}
	row, _ = db.Sponsor.Get(ctx, row.ID)
	if got := SponsorCategory(row, now); got != CategoryCurrent {
		t.Fatalf("with manual time: %s", got)
	}
	if got := SponsorCategory(row, now.AddDate(0, 3, 0)); got != CategoryFormer {
		t.Fatalf("after manual time: %s", got)
	}
}

func TestManualDurationRejectedBeforeSplit(t *testing.T) {
	ctx := context.Background()
	db := openSponsorDB(t)
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	if err := db.Sponsor.Create().SetID("legacy").SetSource(sponsorSchema.SourceAfdian).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	amount, unit, note := 1, "day", "x"
	if _, err := AddManualDuration(ctx, db, "legacy", ManualDurationInput{Amount: &amount, Unit: &unit, Note: &note}, "a", now); err != ErrSponsorNotSplit {
		t.Fatalf("err = %v, want ErrSponsorNotSplit", err)
	}
}

// A webhook that commits while a recompute is between its read and its write
// must not be lost: the compare-and-swap on updated_at forces a re-read.
func TestRecomputeDoesNotLoseConcurrentOrder(t *testing.T) {
	ctx := context.Background()
	db := openSponsorDB(t)
	paidAt := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	recordOrder(t, db, orderJSON("race-user", "o1", "plan", 0, 1, paidAt), paidAt)

	var injected atomic.Bool
	db.Sponsor.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			sm := m.(*postgresql.SponsorMutation)
			if _, set := sm.HasDuration(); set && sm.Op().Is(ent.OpUpdateOne) && injected.CompareAndSwap(false, true) {
				order := mustParseOrder(t, orderJSON("race-user", "o2", "plan", 0, 1, paidAt.Add(time.Hour)), paidAt)
				if _, err := RecordAfdianOrder(ctx, db, order, paidAt); err != nil {
					return nil, err
				}
			}
			return next.Mutate(ctx, m)
		})
	})
	if _, err := RecomputeSponsor(ctx, db, "afdian_race-user", paidAt); err != nil {
		t.Fatal(err)
	}
	row, _ := db.Sponsor.Get(ctx, "afdian_race-user")
	if !injected.Load() || row.AfdianDurationMonths != 2 {
		t.Fatalf("injected=%v months=%d, want the concurrent order counted", injected.Load(), row.AfdianDurationMonths)
	}
}

func TestSyncAfdianSponsorsFetchesOrdersAndProfiles(t *testing.T) {
	ctx := context.Background()
	db := openSponsorDB(t)
	paidAt := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	syncAt := paidAt.AddDate(0, 2, 0)
	renewedEnd := truncateToAfdianDay(syncAt.Add(31 * 24 * time.Hour))

	orders := []map[string]any{
		orderJSON("renewed-user", "o3", "plan", 0, 1, syncAt),
		orderJSON("custom-user", "o4", "", 0, 6, paidAt),
		orderJSON("shop-user", "o5", "item", 1, 1, paidAt),
		orderJSON("lapsed-user", "o1", "plan", 0, 1, paidAt),
		orderJSON("renewed-user", "o2", "plan", 0, 1, paidAt),
	}
	var orderPages atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body map[string]any
		_ = json.UnmarshalRead(r.Body, &body)
		var params map[string]any
		_ = json.Unmarshal([]byte(body["params"].(string)), &params)
		page := int(params["page"].(float64))
		switch {
		case strings.HasSuffix(r.URL.Path, "/query-order"):
			orderPages.Add(1)
			// Two orders per page, newest first.
			start := (page - 1) * 2
			end := min(start+2, len(orders))
			list := []map[string]any{}
			if start < len(orders) {
				list = orders[start:end]
			}
			out, _ := json.Marshal(map[string]any{"ec": 200, "data": map[string]any{"total_page": 3, "list": list}})
			_, _ = w.Write(out)
		case strings.HasSuffix(r.URL.Path, "/query-sponsor"):
			_, _ = fmt.Fprintf(w, `{"ec":200,"data":{"total_page":1,"list":[
				{"user":{"user_id":"lapsed-user","name":"A"},"all_sum_amount":"5.00","last_pay_time":%d,"current_plan":{"name":""}},
				{"user":{"user_id":"renewed-user","name":"B"},"all_sum_amount":"10.00","last_pay_time":%d,"current_plan":{"plan_id":"plan","name":"月度赞助","pay_month":1,"product_type":0,"expire_time":%d}},
				{"user":{"user_id":"custom-user","name":"C"},"all_sum_amount":"30.00","last_pay_time":%d,"current_plan":{"name":"自选方案","expire_time":%d}},
				{"user":{"user_id":"shop-user","name":"D"},"all_sum_amount":"9.00","last_pay_time":%d,"current_plan":{"name":""}}
			]}}`, paidAt.Unix(), syncAt.Unix(), renewedEnd.Unix(), paidAt.Unix(), truncateToAfdianDay(paidAt.Add(6*31*24*time.Hour)).Unix(), paidAt.Unix())
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	cfg := NewAfdianConfig(AfdianConfigOptions{UserID: "dev", APIToken: "token", APIBaseURL: server.URL})

	result, err := SyncAfdianSponsors(ctx, db, cfg, syncAt, SyncOptions{Full: true})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if result.Orders != 5 || result.NewOrders != 5 || result.Imported != 4 {
		t.Fatalf("result = %+v", result)
	}
	want := map[string]Category{
		"afdian_lapsed-user":  CategoryFormer,
		"afdian_renewed-user": CategoryCurrent,
		"afdian_custom-user":  CategoryCurrent,
		"afdian_shop-user":    CategoryOneTime,
	}
	for id, category := range want {
		row, err := db.Sponsor.Get(ctx, id)
		if err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
		if got := SponsorCategory(row, syncAt); got != category {
			t.Fatalf("%s category = %s, want %s", id, got, category)
		}
	}

	// An incremental pass stops at the first page without new orders.
	orderPages.Store(0)
	if _, err := SyncAfdianSponsors(ctx, db, cfg, syncAt, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := orderPages.Load(); got != 1 {
		t.Fatalf("incremental pass fetched %d order pages, want 1", got)
	}
}

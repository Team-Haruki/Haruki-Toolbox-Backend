package sponsor

import (
	"context"
	"encoding/json"
	"fmt"
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
	if _, err := UpsertParsedSponsor(ctx, client, first, true); err != nil {
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
	row, err := UpsertParsedSponsor(ctx, client, second, true)
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
	created, err := UpsertParsedSponsor(ctx, client, order, true)
	if err != nil {
		t.Fatalf("upsert order: %v", err)
	}

	// Admin pins the record and rewrites the display name.
	if _, err := created.Update().SetAfdianSyncDisabled(true).SetName("管理员手动名").SetIsActive(false).Save(ctx); err != nil {
		t.Fatalf("pin sponsor: %v", err)
	}

	// A later sync/webhook for the same user must not touch the pinned record.
	order.Name = "爱发电同步名"
	order.IsActive = true
	row, err := UpsertParsedSponsor(ctx, client, order, true)
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
		Supporters []map[string]json.RawMessage `json:"supporters"`
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

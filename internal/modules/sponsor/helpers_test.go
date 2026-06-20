package sponsor

import (
	"context"
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

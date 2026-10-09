package sponsor

import (
	"context"
	"testing"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	sponsorSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsor"
	manualSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsormanualduration"
)

// legacyFixture is a pre-split sponsors table: one row per situation the
// split has to handle. Orders are stored as a full sync would have stored
// them before the split runs.
type legacyFixtureRow struct {
	id         string
	afdianUser bool
	source     sponsorSchema.Source
	planName   string
	months     *int
	paidAt     *time.Time
	expiresAt  *time.Time
	reported   *time.Time
	orders     []map[string]any
}

var fixtureNow = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

func intPtr(v int) *int { return &v }

func withPlanTitle(order map[string]any, title string) map[string]any {
	order["plan_title"] = title
	return order
}

func legacyFixture() []legacyFixtureRow {
	day := 24 * time.Hour
	paid := func(daysAgo int) time.Time { return fixtureNow.Add(-time.Duration(daysAgo) * day) }
	end := func(start time.Time, months int) time.Time {
		return startOfAfdianDay(start).Add(time.Duration(months*31) * day)
	}
	return []legacyFixtureRow{
		{
			// Active Afdian sponsor, expiry exactly as Afdian reports it.
			id: "afdian_active", afdianUser: true, source: "afdian", planName: "支持一下", months: intPtr(1),
			paidAt: timePtr(paid(10)), expiresAt: timePtr(end(paid(10), 1)), reported: timePtr(end(paid(10), 1)),
			orders: []map[string]any{orderJSON("active", "a1", "plan", 0, 1, paid(10))},
		},
		{
			// Lapsed duration sponsor stored with the "一次性赞助" label
			// (requirement 2): becomes former, no migrated time.
			id: "afdian_lapsed", afdianUser: true, source: "afdian", planName: legacyOneTimePlanName, months: intPtr(1),
			paidAt: timePtr(paid(120)), expiresAt: timePtr(end(paid(120), 1)),
			orders: []map[string]any{withPlanTitle(orderJSON("lapsed", "l1", "plan", 0, 1, paid(120)), "支持一下")},
		},
		{
			// An admin pushed the expiry 60 days past what Afdian gave.
			id: "afdian_extended", afdianUser: true, source: "afdian", planName: "强烈支持一下", months: intPtr(1),
			paidAt: timePtr(paid(40)), expiresAt: timePtr(end(paid(40), 1).Add(60 * day)),
			orders: []map[string]any{orderJSON("extended", "e1", "plan", 0, 1, paid(40))},
		},
		{
			// Legacy webhook expiry: paid_at + one calendar month (31 days in
			// this month) without the midnight truncation; hours longer.
			id: "afdian_calendar", afdianUser: true, source: "afdian", planName: "支持一下", months: intPtr(1),
			paidAt: timePtr(time.Date(2026, 8, 1, 20, 0, 0, 0, utc8)), expiresAt: timePtr(time.Date(2026, 9, 1, 20, 0, 0, 0, utc8)),
			orders: []map[string]any{orderJSON("calendar", "c1", "plan", 0, 1, time.Date(2026, 8, 1, 20, 0, 0, 0, utc8))},
		},
		{
			// 自选方案 sponsor the old code labelled one-time.
			id: "afdian_custom", afdianUser: true, source: "afdian", planName: legacyOneTimePlanName,
			paidAt: timePtr(paid(5)),
			orders: []map[string]any{orderJSON("custom", "s1", "", 0, 3, paid(5))},
		},
		{
			// Only a sale-plan purchase: stays one-time.
			id: "afdian_shop", afdianUser: true, source: "afdian", planName: legacyOneTimePlanName,
			paidAt: timePtr(paid(30)),
			orders: []map[string]any{orderJSON("shop", "p1", "item", 1, 1, paid(30))},
		},
		{
			// Hand-entered supporter with an expiry still running.
			id: "manual_running", source: "manual", planName: "线下赞助",
			paidAt: timePtr(paid(20)), expiresAt: timePtr(paid(20).Add(90 * day)),
		},
		{
			// Imported supporter whose time ran out long ago.
			id: "legacy_past", source: "legacy", planName: "旧赞助",
			expiresAt: timePtr(paid(400)),
		},
		{
			// Hand-entered supporter without any time.
			id: "manual_no_time", source: "manual", planName: "感谢",
		},
	}
}

func seedLegacyFixture(t *testing.T, db *postgresql.Client) {
	t.Helper()
	ctx := context.Background()
	for _, row := range legacyFixture() {
		create := db.Sponsor.Create().
			SetID(row.id).
			SetSource(row.source).
			SetPlanName(row.planName).
			SetNillablePlanPayMonths(row.months).
			SetNillablePaidAt(row.paidAt).
			SetNillablePlanExpiresAt(row.expiresAt).
			SetCreatedAt(fixtureNow.AddDate(-2, 0, 0))
		if row.afdianUser {
			create.SetAfdianUserID(row.id)
		}
		if row.reported != nil {
			create.SetAfdianReportedExpiresAt(*row.reported).SetAfdianReportedAt(fixtureNow.Add(-time.Hour))
		}
		if err := create.Exec(ctx); err != nil {
			t.Fatalf("seed %s: %v", row.id, err)
		}
		for _, raw := range row.orders {
			order := mustParseOrder(t, raw, fixtureNow)
			if err := db.SponsorAfdianOrder.Create().
				SetID(order.OutTradeNo).SetSponsorID(row.id).SetAfdianUserID(order.AfdianUserID).
				SetPlanID(order.PlanID).SetPlanTitle(order.PlanTitle).SetProductType(order.ProductType).SetMonth(order.Month).
				SetStatus(order.Status).SetPaidAt(order.PaidAt).Exec(ctx); err != nil {
				t.Fatalf("seed order %s: %v", order.OutTradeNo, err)
			}
		}
	}
}

// TestSplitLegacySponsorDurationsDryRun is the dry run against the fixture:
// it prints the per-category counts and the migrated entries without writing.
func TestSplitLegacySponsorDurationsDryRun(t *testing.T) {
	ctx := context.Background()
	db := openSponsorDB(t)
	seedLegacyFixture(t, db)

	report, err := SplitLegacySponsorDurations(ctx, db, fixtureNow, SplitOptions{OrdersComplete: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("dry run on the legacy fixture:\n  %s", report)
	for _, c := range report.Cases {
		t.Logf("  migrated: %s legacy=%s recomputed=%v days=%d under_one_day=%v", c.SponsorID, c.LegacyExpiresAt.Format(time.RFC3339), c.RecomputedExpiresAt, c.MigratedDays, c.UnderOneDay)
	}

	if report.Rows != 9 || report.Split != 9 || report.MigratedEntries != 4 || report.MislabeledOneTime != 2 {
		t.Fatalf("report = %s", report)
	}
	want := map[Category]int{CategoryCurrent: 4, CategoryFormer: 5}
	for category, n := range want {
		if report.Categories[category] != n {
			t.Fatalf("after: %s = %d, want %d (%v)", category, report.Categories[category], n, report.Categories)
		}
	}
	if pending, _ := HasUnsplitSponsors(ctx, db); !pending {
		t.Fatalf("dry run wrote to the database")
	}
	if n, _ := db.SponsorManualDuration.Query().Count(ctx); n != 0 {
		t.Fatalf("dry run created %d entries", n)
	}
}

func TestSplitLegacySponsorDurationsNeverShortensAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := openSponsorDB(t)
	seedLegacyFixture(t, db)
	legacy := map[string]*time.Time{}
	for _, row := range legacyFixture() {
		legacy[row.id] = row.expiresAt
	}

	report, err := SplitLegacySponsorDurations(ctx, db, fixtureNow, SplitOptions{OrdersComplete: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Split != 9 || report.MigratedEntries != 4 {
		t.Fatalf("report = %s", report)
	}
	rows, _ := db.Sponsor.Query().All(ctx)
	for _, row := range rows {
		if row.DurationSplitAt == nil {
			t.Fatalf("%s not split", row.ID)
		}
		if old := legacy[row.ID]; old != nil && (row.PlanExpiresAt == nil || row.PlanExpiresAt.Before(*old)) {
			t.Fatalf("%s shortened: %v -> %v", row.ID, old, row.PlanExpiresAt)
		}
	}

	extended, _ := db.Sponsor.Get(ctx, "afdian_extended")
	if !extended.PlanExpiresAt.Equal(extended.AfdianExpiresAt.Add(60 * 24 * time.Hour)) {
		t.Fatalf("extended: effective %v, afdian %v", extended.PlanExpiresAt, extended.AfdianExpiresAt)
	}
	entry, err := db.SponsorManualDuration.Query().Where(manualSchema.SponsorIDEQ("afdian_extended")).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Note != MigratedManualNote || entry.Origin != manualSchema.OriginMigration || entry.Amount != 60 || entry.Unit != manualSchema.UnitDay {
		t.Fatalf("migrated entry = %+v", entry)
	}
	lapsed, _ := db.Sponsor.Get(ctx, "afdian_lapsed")
	if stringPtrValue(lapsed.PlanName) != "支持一下" || SponsorCategory(lapsed, fixtureNow) != CategoryFormer {
		t.Fatalf("lapsed: plan=%v category=%s", lapsed.PlanName, SponsorCategory(lapsed, fixtureNow))
	}
	custom, _ := db.Sponsor.Get(ctx, "afdian_custom")
	if SponsorCategory(custom, fixtureNow) != CategoryCurrent || custom.AfdianDurationMonths != 3 || stringPtrValue(custom.PlanName) != customPlanName {
		t.Fatalf("custom plan: %s months=%d", SponsorCategory(custom, fixtureNow), custom.AfdianDurationMonths)
	}

	again, err := SplitLegacySponsorDurations(ctx, db, fixtureNow.Add(time.Hour), SplitOptions{OrdersComplete: true})
	if err != nil {
		t.Fatal(err)
	}
	if again.Split != 0 || again.MigratedEntries != 0 || again.AlreadySplit != 9 {
		t.Fatalf("second run = %s", again)
	}
	if n, _ := db.SponsorManualDuration.Query().Count(ctx); n != 4 {
		t.Fatalf("entries after second run = %d, want 4", n)
	}
}

func TestSplitWaitsForCompleteOrderHistory(t *testing.T) {
	ctx := context.Background()
	db := openSponsorDB(t)
	seedLegacyFixture(t, db)
	report, err := SplitLegacySponsorDurations(ctx, db, fixtureNow, SplitOptions{OrdersComplete: false})
	if err != nil {
		t.Fatal(err)
	}
	// Only the three rows without an Afdian account can be split.
	if report.Split != 3 || report.SkippedIncompleteHistory != 6 {
		t.Fatalf("report = %s", report)
	}
	row, _ := db.Sponsor.Get(ctx, "afdian_extended")
	if row.DurationSplitAt != nil {
		t.Fatalf("Afdian row split without its order history")
	}
}

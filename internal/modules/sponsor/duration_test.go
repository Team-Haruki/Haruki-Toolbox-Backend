package sponsor

import (
	"testing"
	"time"
)

var utc8 = time.FixedZone("UTC+8", 8*60*60)

func durationOrder(no string, planID string, productType int, month int, paidAt time.Time) AfdianOrderFacts {
	return AfdianOrderFacts{OutTradeNo: no, PlanID: planID, ProductType: productType, Month: month, Status: afdianOrderStatusPaid, PaidAt: paidAt}
}

func timePtr(t time.Time) *time.Time { return &t }

func TestClassifyAfdianOrder(t *testing.T) {
	paidAt := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		order AfdianOrderFacts
		want  AfdianOrderKind
	}{
		{"regular plan", durationOrder("a", "plan", 0, 1, paidAt), AfdianOrderDuration},
		{"自选方案: no plan_id, sponsor-chosen months", durationOrder("b", "", 0, 3, paidAt), AfdianOrderDuration},
		{"售卖方案 is one-time even with a month", durationOrder("c", "plan", 1, 1, paidAt), AfdianOrderOneTime},
		{"zero month is one-time", durationOrder("d", "", 0, 0, paidAt), AfdianOrderOneTime},
		{"unpaid is ignored", AfdianOrderFacts{OutTradeNo: "e", PlanID: "plan", Month: 1, Status: 1, PaidAt: paidAt}, AfdianOrderIgnored},
	}
	for _, tc := range cases {
		if got := ClassifyAfdianOrder(tc.order); got != tc.want {
			t.Errorf("%s: kind = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestComputeAfdianPeriodCountsThirtyOneDaysToMidnightUTC8(t *testing.T) {
	// 2026-06-20 20:00 UTC+8 + 31 days = 2026-07-21 20:00, truncated to 00:00.
	paidAt := time.Date(2026, 6, 20, 20, 0, 0, 0, utc8)
	period := ComputeAfdianPeriod([]AfdianOrderFacts{durationOrder("a", "plan", 0, 1, paidAt)}, AfdianReport{})
	want := time.Date(2026, 7, 21, 0, 0, 0, 0, utc8)
	if period.End == nil || !period.End.Equal(want) {
		t.Fatalf("end = %v, want %v", period.End, want)
	}
	if period.Months != 1 || period.DurationOrders != 1 {
		t.Fatalf("months/orders = %d/%d", period.Months, period.DurationOrders)
	}
}

// afdianDays is n days of Afdian validity.
func afdianDays(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

// The fixtures below are anonymised copies of the patterns in Afdian's own
// order export; each want is the export's 有效期结束 + 1 s (ends are exclusive).
func TestComputeAfdianPeriodMatchesAfdianValidity(t *testing.T) {
	day := func(y int, m time.Month, d, h int) time.Time { return time.Date(y, m, d, h, 17, 5, 0, utc8) }
	midnight := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, utc8) }
	cases := []struct {
		name   string
		orders []AfdianOrderFacts
		want   time.Time
		months int
	}{
		{
			name: "same-plan early renewal continues the running order",
			orders: []AfdianOrderFacts{
				durationOrder("a1", "plan-a", 0, 3, day(2026, 5, 16, 9)),
				durationOrder("a2", "plan-a", 0, 3, day(2026, 7, 30, 21)),
			},
			want:   midnight(2026, 5, 16).Add(afdianDays(186)),
			months: 6,
		},
		{
			name: "6m then 1m of the same plan continue across months",
			orders: []AfdianOrderFacts{
				durationOrder("b1", "plan-b", 0, 6, day(2026, 5, 15, 22)),
				durationOrder("b2", "plan-b", 0, 1, day(2026, 9, 1, 8)),
			},
			want:   midnight(2026, 5, 15).Add(afdianDays(217)),
			months: 7,
		},
		{
			name: "two 自选方案 orders on the same day share one validity",
			orders: []AfdianOrderFacts{
				durationOrder("c1", "", 0, 1, day(2026, 5, 18, 10)),
				durationOrder("c2", "", 0, 1, day(2026, 5, 18, 11)),
			},
			want:   midnight(2026, 5, 18).Add(afdianDays(31)),
			months: 2,
		},
		{
			name: "a 自选方案 order while a fixed plan runs starts on its own day",
			orders: []AfdianOrderFacts{
				durationOrder("d1", "plan-a", 0, 1, day(2026, 5, 18, 9)),
				durationOrder("d2", "", 0, 1, day(2026, 6, 8, 12)),
			},
			want:   midnight(2026, 6, 8).Add(afdianDays(31)),
			months: 2,
		},
		{
			name: "fixed 1m and 自选方案 36m on the same day: the later end wins",
			orders: []AfdianOrderFacts{
				durationOrder("e1", "plan-c", 0, 1, day(2026, 5, 15, 9)),
				durationOrder("e2", "", 0, 36, day(2026, 5, 15, 9).Add(time.Minute)),
			},
			want:   midnight(2026, 5, 15).Add(afdianDays(36 * 31)),
			months: 37,
		},
		{
			name: "a renewal after the plan lapsed restarts on its payment day",
			orders: []AfdianOrderFacts{
				durationOrder("f1", "plan-a", 0, 1, day(2026, 1, 1, 9)),
				durationOrder("f2", "plan-a", 0, 2, day(2026, 6, 1, 23)),
			},
			want:   midnight(2026, 6, 1).Add(afdianDays(62)),
			months: 3,
		},
		{
			name: "a different plan does not continue the running one",
			orders: []AfdianOrderFacts{
				durationOrder("g1", "plan-a", 0, 3, day(2026, 5, 1, 9)),
				durationOrder("g2", "plan-b", 0, 1, day(2026, 5, 10, 9)),
			},
			want:   midnight(2026, 5, 1).Add(afdianDays(93)),
			months: 4,
		},
		{
			name: "a sale-plan order grants no time",
			orders: []AfdianOrderFacts{
				durationOrder("h1", "plan-a", 0, 1, day(2026, 5, 1, 9)),
				durationOrder("h2", "item", afdianProductTypeForSale, 1, day(2026, 5, 2, 9)),
			},
			want:   midnight(2026, 5, 1).Add(afdianDays(31)),
			months: 1,
		},
	}
	for _, tc := range cases {
		// Input order must not matter: payment time decides.
		reversed := make([]AfdianOrderFacts, 0, len(tc.orders))
		for i := len(tc.orders) - 1; i >= 0; i-- {
			reversed = append(reversed, tc.orders[i])
		}
		period := ComputeAfdianPeriod(reversed, AfdianReport{})
		if period.End == nil || !period.End.Equal(tc.want) {
			t.Errorf("%s: end = %v, want %v", tc.name, period.End, tc.want.UTC())
		}
		if period.Months != tc.months {
			t.Errorf("%s: months = %d, want %d", tc.name, period.Months, tc.months)
		}
	}
}

func TestComputeAfdianPeriodUsesOnlyALaterFreshReport(t *testing.T) {
	paidAt := time.Date(2026, 1, 1, 10, 0, 0, 0, utc8)
	orders := []AfdianOrderFacts{durationOrder("a", "plan", 0, 1, paidAt)}
	derived := time.Date(2026, 2, 1, 0, 0, 0, 0, utc8)
	later := time.Date(2026, 3, 1, 0, 0, 0, 0, utc8)
	earlier := time.Date(2026, 1, 20, 0, 0, 0, 0, utc8)
	fresh := timePtr(paidAt.Add(time.Hour))

	if got := ComputeAfdianPeriod(orders, AfdianReport{ExpiresAt: &later, ObservedAt: fresh}); !got.End.Equal(later) {
		t.Fatalf("later fresh report: %v, want %v", got.End, later)
	}
	if got := ComputeAfdianPeriod(orders, AfdianReport{ExpiresAt: &earlier, ObservedAt: fresh}); !got.End.Equal(derived) {
		t.Fatalf("earlier report must not cut the order-derived time: %v", got.End)
	}
	if got := ComputeAfdianPeriod(orders, AfdianReport{ExpiresAt: &later, ObservedAt: timePtr(paidAt.Add(-time.Hour))}); !got.End.Equal(derived) {
		t.Fatalf("stale report used: %v", got.End)
	}
	if got := ComputeAfdianPeriod(nil, AfdianReport{ExpiresAt: &later, ObservedAt: fresh}); got.HasDuration() {
		t.Fatalf("a report without duration orders granted time: %+v", got)
	}
	none := ComputeAfdianPeriod([]AfdianOrderFacts{durationOrder("b", "", 1, 1, paidAt)}, AfdianReport{})
	if none.HasDuration() || none.OneTimeOrders != 1 {
		t.Fatalf("one-time only: %+v", none)
	}
}

func TestComputeEffectiveExpiryStacksManualAfterAfdian(t *testing.T) {
	afdianEnd := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	// Recorded while Afdian time runs: starts where Afdian ends.
	during := []ManualDurationFacts{{ID: 1, Amount: 10, Unit: ManualUnitDay, StartsAt: afdianEnd.Add(-20 * day)}}
	if got := ComputeEffectiveExpiry(&afdianEnd, during); got == nil || !got.Equal(afdianEnd.Add(10*day)) {
		t.Fatalf("manual during Afdian: %v", got)
	}

	// Two entries run one after the other; a month is 31 days.
	seq := []ManualDurationFacts{
		{ID: 2, Amount: 1, Unit: ManualUnitMonth, StartsAt: afdianEnd.Add(-day)},
		{ID: 1, Amount: 5, Unit: ManualUnitDay, StartsAt: afdianEnd.Add(-2 * day)},
	}
	if got := ComputeEffectiveExpiry(&afdianEnd, seq); got == nil || !got.Equal(afdianEnd.Add(36*day)) {
		t.Fatalf("sequence: %v", got)
	}

	// Recorded after the sponsor lapsed: counts from its own start.
	later := afdianEnd.Add(100 * day)
	gap := []ManualDurationFacts{{ID: 3, Amount: 30, Unit: ManualUnitDay, StartsAt: later}}
	if got := ComputeEffectiveExpiry(&afdianEnd, gap); got == nil || !got.Equal(later.Add(30*day)) {
		t.Fatalf("after lapse: %v", got)
	}

	// Manual only.
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := ComputeEffectiveExpiry(nil, []ManualDurationFacts{{ID: 4, Amount: 2, Unit: ManualUnitMonth, StartsAt: start}}); got == nil || !got.Equal(start.Add(62*day)) {
		t.Fatalf("manual only: %v", got)
	}
	if got := ComputeEffectiveExpiry(nil, nil); got != nil {
		t.Fatalf("nothing: %v", got)
	}
}

func TestCategoryForBoundaries(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name        string
		hasDuration bool
		expires     *time.Time
		want        Category
	}{
		{"expires in the future", true, timePtr(now.Add(time.Second)), CategoryCurrent},
		{"expires exactly now", true, timePtr(now), CategoryFormer},
		{"expired", true, timePtr(now.Add(-time.Second)), CategoryFormer},
		{"only one-time orders", false, nil, CategoryOneTime},
	}
	for _, tc := range cases {
		if got := CategoryFor(tc.hasDuration, tc.expires, now); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestCategoryForMixedAndManualOnlySponsors(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	paidAt := now.AddDate(0, -3, 0)

	// One-time purchase plus an expired subscription: former, not one-time.
	mixed := ComputeAfdianPeriod([]AfdianOrderFacts{
		durationOrder("sub", "plan", 0, 1, paidAt),
		durationOrder("item", "plan", 1, 1, now.Add(-time.Hour)),
	}, AfdianReport{})
	if got := CategoryFor(mixed.HasDuration(), ComputeEffectiveExpiry(mixed.End, nil), now); got != CategoryFormer {
		t.Fatalf("mixed: %s", got)
	}

	// Manual time only, still running, then lapsed.
	entry := []ManualDurationFacts{{ID: 1, Amount: 30, Unit: ManualUnitDay, StartsAt: now.Add(-24 * time.Hour)}}
	if got := CategoryFor(true, ComputeEffectiveExpiry(nil, entry), now); got != CategoryCurrent {
		t.Fatalf("manual-only running: %s", got)
	}
	if got := CategoryFor(true, ComputeEffectiveExpiry(nil, entry), now.AddDate(0, 2, 0)); got != CategoryFormer {
		t.Fatalf("manual-only lapsed: %s", got)
	}

	// Expired Afdian time extended by manual time: current.
	extended := ComputeEffectiveExpiry(mixed.End, []ManualDurationFacts{{ID: 2, Amount: 1, Unit: ManualUnitMonth, StartsAt: paidAt}})
	if got := CategoryFor(true, extended, now); got != CategoryFormer {
		// paidAt+31d floored, +31d: still before now (3 months later).
		t.Fatalf("extended but still expired: %s", got)
	}
	extendedNow := ComputeEffectiveExpiry(mixed.End, []ManualDurationFacts{{ID: 2, Amount: 1, Unit: ManualUnitMonth, StartsAt: now}})
	if got := CategoryFor(true, extendedNow, now); got != CategoryCurrent {
		t.Fatalf("manual entry recorded today: %s", got)
	}
}

func TestLeavesFutureGap(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	afdianEnd := now.Add(10 * day)
	lapsed := now.Add(-10 * day)
	entry := func(id int, start time.Time) ManualDurationFacts {
		return ManualDurationFacts{ID: id, Amount: 5, Unit: ManualUnitDay, StartsAt: start}
	}
	cases := []struct {
		name    string
		end     *time.Time
		entries []ManualDurationFacts
		want    bool
	}{
		{"recorded today after a lapse", &lapsed, []ManualDurationFacts{entry(1, now)}, false},
		{"future start inside Afdian time", &afdianEnd, []ManualDurationFacts{entry(1, now.Add(5*day))}, false},
		{"future start right at the Afdian end", &afdianEnd, []ManualDurationFacts{entry(1, afdianEnd)}, false},
		{"future start after a lapse", &lapsed, []ManualDurationFacts{entry(1, now.Add(day))}, true},
		{"future start after the Afdian end", &afdianEnd, []ManualDurationFacts{entry(1, afdianEnd.Add(day))}, true},
		{"manual only, future start", nil, []ManualDurationFacts{entry(1, now.Add(time.Hour))}, true},
		{"chained entries stay contiguous", &afdianEnd, []ManualDurationFacts{entry(1, now), entry(2, afdianEnd.Add(3*day))}, false},
	}
	for _, tc := range cases {
		if got := LeavesFutureGap(tc.end, tc.entries, now); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

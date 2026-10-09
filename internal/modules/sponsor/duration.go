package sponsor

import (
	"sort"
	"time"
)

// Sponsor duration model.
//
// A sponsor's time comes from two sources:
//
//   - Afdian time, recomputed from the stored Afdian orders
//     (sponsor_afdian_orders) by ComputeAfdianPeriod;
//   - manual time, the admin's sponsor_manual_durations entries, for support
//     received outside Afdian.
//
// The effective expiry (ComputeEffectiveExpiry) is the Afdian time followed by
// the manual entries in sequence, so the total time is the Afdian time plus the
// manual time with no overlap lost. SponsorCategory turns it into one of three
// mutually exclusive categories. These functions are the only place that
// classifies orders, merges the sources or decides a category; the public
// wall, the admin list and the migration all call them.

// Category is the sponsor-wall bucket a sponsor falls into.
type Category string

const (
	// CategoryCurrent (当前赞助): the effective expiry is still in the future.
	CategoryCurrent Category = "current"
	// CategoryFormer (曾经赞助): has had duration (Afdian or manual) but the
	// effective expiry is at or before now.
	CategoryFormer Category = "former"
	// CategoryOneTime (一次性赞助): never had any duration, only one-time
	// purchases (or a hand-entered supporter without time).
	CategoryOneTime Category = "one_time"
)

// AfdianOrderKind is how an Afdian order counts toward duration.
type AfdianOrderKind int

const (
	// AfdianOrderIgnored is an order that is not paid; it grants nothing.
	AfdianOrderIgnored AfdianOrderKind = iota
	// AfdianOrderOneTime is a paid order that grants no time.
	AfdianOrderOneTime
	// AfdianOrderDuration is a paid order that grants `month` months.
	AfdianOrderDuration
)

// Afdian order field values, from the developer guide
// (https://guide.afdian.com/creator/developer, 查询订单 / Webhook):
//
//	status        "2 表示交易成功"; only status 2 is pushed or counted.
//	product_type  "0表示常规方案 1表示售卖方案".
//	plan_id       "方案ID，如自选，则为空".
//	month         "赞助月份".
const (
	afdianOrderStatusPaid      = 2
	afdianProductTypeRegular   = 0
	afdianProductTypeForSale   = 1
	afdianMonthDays            = 31
	afdianExpiryTimezoneOffset = 8 * 60 * 60
)

// afdianExpiryZone is the zone Afdian truncates plan expiries to: every
// current_plan.expire_time it reports is 00:00 UTC+8.
var afdianExpiryZone = time.FixedZone("UTC+8", afdianExpiryTimezoneOffset)

// AfdianOrderFacts are the stored fields of one Afdian order.
type AfdianOrderFacts struct {
	OutTradeNo  string
	PlanID      string
	ProductType int
	Month       int
	Status      int
	PaidAt      time.Time
}

// ClassifyAfdianOrder is the single rule for one-time versus duration orders:
//
//   - status != 2: not a completed payment, ignored;
//   - product_type == 1 (售卖方案, a sale/merchandise plan): one-time;
//   - month <= 0: one-time, there is no time to grant;
//   - otherwise (常规方案, product_type 0 or absent): duration of `month`
//     months. This includes 自选方案 orders, whose plan_id is empty: the sponsor
//     chooses both the amount and the number of months, and the order's own
//     month field carries that choice. Afdian reports such sponsors with a
//     current_plan named 自选方案 that has an expire_time, like any plan.
//
// The plan price is never used to infer time.
func ClassifyAfdianOrder(order AfdianOrderFacts) AfdianOrderKind {
	if order.Status != afdianOrderStatusPaid {
		return AfdianOrderIgnored
	}
	if order.ProductType == afdianProductTypeForSale {
		return AfdianOrderOneTime
	}
	if order.Month <= 0 {
		return AfdianOrderOneTime
	}
	return AfdianOrderDuration
}

// AfdianPeriod is the Afdian part of a sponsor's time.
type AfdianPeriod struct {
	// End of the Afdian time, nil when Afdian never granted any.
	End *time.Time
	// Months granted by duration orders.
	Months int
	// DurationOrders and OneTimeOrders count the paid orders of each kind.
	DurationOrders int
	OneTimeOrders  int
	// LatestDuration is the newest duration order, nil without one.
	LatestDuration *AfdianOrderFacts
}

// HasDuration reports whether Afdian ever granted time.
func (p AfdianPeriod) HasDuration() bool {
	return p.End != nil
}

// AfdianReport is the plan expiry Afdian itself last reported for a sponsor
// (query-sponsor current_plan.expire_time) and when it was observed.
type AfdianReport struct {
	ExpiresAt  *time.Time
	ObservedAt *time.Time
}

// ComputeAfdianPeriod recomputes the Afdian time from the order history, the
// way Afdian computes a plan's expiry: duration orders are applied in payment
// order, each starting at the later of its payment time and the end of the
// previous one, adding 31 days per month, and the end is truncated to 00:00
// UTC+8. (Checked against every live current_plan.expire_time when this was
// written: the orders reproduce Afdian's expiry to the second in all but one
// case.)
//
// When Afdian has reported an expiry that was observed after the newest
// duration order was paid, that report already accounts for every order and
// is used instead, so Afdian stays authoritative about its own time.
func ComputeAfdianPeriod(orders []AfdianOrderFacts, report AfdianReport) AfdianPeriod {
	sorted := append([]AfdianOrderFacts(nil), orders...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].PaidAt.Equal(sorted[j].PaidAt) {
			return sorted[i].PaidAt.Before(sorted[j].PaidAt)
		}
		return sorted[i].OutTradeNo < sorted[j].OutTradeNo
	})

	var period AfdianPeriod
	for i := range sorted {
		order := sorted[i]
		switch ClassifyAfdianOrder(order) {
		case AfdianOrderOneTime:
			period.OneTimeOrders++
			continue
		case AfdianOrderIgnored:
			continue
		}
		start := order.PaidAt
		if period.End != nil && period.End.After(start) {
			start = *period.End
		}
		end := truncateToAfdianDay(start.Add(time.Duration(order.Month) * afdianMonthDays * 24 * time.Hour))
		period.End = &end
		period.Months += order.Month
		period.DurationOrders++
		period.LatestDuration = &sorted[i]
	}

	if report.ExpiresAt != nil && report.ObservedAt != nil {
		if period.LatestDuration == nil || !report.ObservedAt.Before(period.LatestDuration.PaidAt) {
			reported := report.ExpiresAt.UTC()
			period.End = &reported
		}
	}
	if period.End != nil {
		end := period.End.UTC()
		period.End = &end
	}
	return period
}

func truncateToAfdianDay(t time.Time) time.Time {
	local := t.In(afdianExpiryZone)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, afdianExpiryZone).UTC()
}

// ManualUnit is the unit of a manual duration entry.
type ManualUnit string

const (
	ManualUnitDay   ManualUnit = "day"
	ManualUnitMonth ManualUnit = "month"
)

// ManualDurationFacts are the fields of one manual entry that affect time.
type ManualDurationFacts struct {
	ID       int
	Amount   int
	Unit     ManualUnit
	StartsAt time.Time
}

// Length is the time the entry grants; a month is 31 days, as on Afdian.
func (m ManualDurationFacts) Length() time.Duration {
	if m.Amount <= 0 {
		return 0
	}
	days := m.Amount
	if m.Unit == ManualUnitMonth {
		days *= afdianMonthDays
	}
	return time.Duration(days) * 24 * time.Hour
}

// ComputeEffectiveExpiry merges the two sources. The manual entries follow
// the Afdian time contiguously: they are applied in starts_at order (then by
// id), each starting at the later of its own starts_at and the end of
// everything before it, beginning with the end of the Afdian time. A manual
// entry therefore never overlaps Afdian time or another entry, and an entry
// recorded after the sponsor had lapsed starts on its own date instead of
// being spent on the past. Without manual entries the result is the Afdian
// end; without either it is nil.
func ComputeEffectiveExpiry(afdianEnd *time.Time, entries []ManualDurationFacts) *time.Time {
	sorted := append([]ManualDurationFacts(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].StartsAt.Equal(sorted[j].StartsAt) {
			return sorted[i].StartsAt.Before(sorted[j].StartsAt)
		}
		return sorted[i].ID < sorted[j].ID
	})

	var cursor *time.Time
	if afdianEnd != nil {
		end := afdianEnd.UTC()
		cursor = &end
	}
	for _, entry := range sorted {
		length := entry.Length()
		if length <= 0 {
			continue
		}
		start := entry.StartsAt.UTC()
		if cursor != nil && cursor.After(start) {
			start = *cursor
		}
		end := start.Add(length)
		cursor = &end
	}
	return cursor
}

// CategoryFor decides the category from the merged duration. A sponsor whose
// effective expiry equals now has expired.
func CategoryFor(hasDuration bool, effectiveExpiresAt *time.Time, now time.Time) Category {
	if effectiveExpiresAt != nil && effectiveExpiresAt.After(now) {
		return CategoryCurrent
	}
	if hasDuration || effectiveExpiresAt != nil {
		return CategoryFormer
	}
	return CategoryOneTime
}

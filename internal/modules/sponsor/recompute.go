package sponsor

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	sponsorSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsor"
	orderSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsorafdianorder"
	manualSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsormanualduration"
)

const recomputeAttempts = 5

var errSponsorUpdateConflict = errors.New("sponsor changed during recompute; retry required")

// SponsorDurations is everything the duration model derives for one sponsor.
type SponsorDurations struct {
	Afdian             AfdianPeriod
	Manual             []ManualDurationFacts
	EffectiveExpiresAt *time.Time
	HasDuration        bool
	PaidOrders         int
	// LatestPlanName is the plan of the newest duration order: its
	// plan_title, or 自选方案 (Afdian's name for it) when it has no plan_id.
	LatestPlanName string
}

// LoadSponsorDurations reads a sponsor's orders and manual entries and merges
// them. It does not write.
func LoadSponsorDurations(ctx context.Context, db *postgresql.Client, row *postgresql.Sponsor) (SponsorDurations, error) {
	orders, err := db.SponsorAfdianOrder.Query().Where(orderSchema.SponsorIDEQ(row.ID)).All(ctx)
	if err != nil {
		return SponsorDurations{}, err
	}
	entries, err := db.SponsorManualDuration.Query().Where(manualSchema.SponsorIDEQ(row.ID)).All(ctx)
	if err != nil {
		return SponsorDurations{}, err
	}
	return MergeSponsorDurations(row, orders, entries), nil
}

func MergeSponsorDurations(row *postgresql.Sponsor, orders []*postgresql.SponsorAfdianOrder, entries []*postgresql.SponsorManualDuration) SponsorDurations {
	facts := make([]AfdianOrderFacts, 0, len(orders))
	paid := 0
	for _, order := range orders {
		fact := OrderFacts(order)
		if ClassifyAfdianOrder(fact) != AfdianOrderIgnored {
			paid++
		}
		facts = append(facts, fact)
	}
	afdian := ComputeAfdianPeriod(facts, AfdianReport{ExpiresAt: row.AfdianReportedExpiresAt, ObservedAt: row.AfdianReportedAt})
	manual := make([]ManualDurationFacts, 0, len(entries))
	hasManual := false
	for _, entry := range entries {
		fact := manualFacts(entry)
		if fact.Length() > 0 {
			hasManual = true
		}
		manual = append(manual, fact)
	}
	latestPlanName := ""
	if latest := afdian.LatestDuration; latest != nil {
		for _, order := range orders {
			if order.ID != latest.OutTradeNo {
				continue
			}
			latestPlanName = strings.TrimSpace(order.PlanTitle)
			if latestPlanName == "" && order.PlanID == "" {
				latestPlanName = customPlanName
			}
		}
	}
	return SponsorDurations{
		LatestPlanName:     latestPlanName,
		Afdian:             afdian,
		Manual:             manual,
		EffectiveExpiresAt: ComputeEffectiveExpiry(afdian.End, manual),
		HasDuration:        afdian.HasDuration() || hasManual,
		PaidOrders:         paid,
	}
}

func OrderFacts(order *postgresql.SponsorAfdianOrder) AfdianOrderFacts {
	return AfdianOrderFacts{
		OutTradeNo:  order.ID,
		PlanID:      order.PlanID,
		ProductType: order.ProductType,
		Month:       order.Month,
		Status:      order.Status,
		PaidAt:      order.PaidAt,
	}
}

func manualFacts(entry *postgresql.SponsorManualDuration) ManualDurationFacts {
	return ManualDurationFacts{
		ID:       entry.ID,
		Amount:   entry.Amount,
		Unit:     ManualUnit(entry.Unit),
		StartsAt: entry.StartsAt,
	}
}

// RecomputeSponsor rewrites the cached duration columns of one sponsor from
// its orders and manual entries. Rows that still carry the legacy single
// expiry are left alone until SplitLegacySponsorDurations has split them. The
// update is a compare-and-swap on updated_at, so a concurrent order or entry
// change makes it re-read instead of writing a stale result.
func RecomputeSponsor(ctx context.Context, db *postgresql.Client, sponsorID string, now time.Time) (*postgresql.Sponsor, error) {
	for attempt := 0; attempt < recomputeAttempts; attempt++ {
		row, err := db.Sponsor.Get(ctx, sponsorID)
		if err != nil {
			return nil, err
		}
		if row.DurationSplitAt == nil {
			return row, nil
		}
		durations, err := LoadSponsorDurations(ctx, db, row)
		if err != nil {
			return nil, err
		}
		saved, err := saveRecomputed(ctx, db, row, durations, now)
		if errors.Is(err, errSponsorUpdateConflict) {
			continue
		}
		return saved, err
	}
	return nil, errSponsorUpdateConflict
}

func saveRecomputed(ctx context.Context, db *postgresql.Client, row *postgresql.Sponsor, d SponsorDurations, now time.Time) (*postgresql.Sponsor, error) {
	update := db.Sponsor.UpdateOneID(row.ID).
		Where(sponsorSchema.UpdatedAtEQ(row.UpdatedAt)).
		SetAfdianDurationMonths(d.Afdian.Months).
		SetHasDuration(d.HasDuration).
		SetIsActive(CategoryFor(d.EffectiveExpiresAt, now) == CategoryCurrent)
	if d.Afdian.End != nil {
		update.SetAfdianExpiresAt(*d.Afdian.End)
	} else {
		update.ClearAfdianExpiresAt()
	}
	if d.EffectiveExpiresAt != nil {
		update.SetPlanExpiresAt(*d.EffectiveExpiresAt)
	} else {
		update.ClearPlanExpiresAt()
	}
	if latest := d.Afdian.LatestDuration; latest != nil {
		update.SetPlanPayMonths(latest.Month)
		if row.PaidAt == nil || latest.PaidAt.After(*row.PaidAt) {
			update.SetPaidAt(latest.PaidAt)
		}
	}
	if d.PaidOrders > 0 {
		update.SetSupportCount(d.PaidOrders)
	}
	// Repair the leftover of the pre-split bug: a lapsed plan was renamed
	// "一次性赞助". It is replaced by the plan of the newest duration order
	// (or cleared, which shows the default label). Pinned profiles were never
	// overwritten, so they are kept.
	if !row.AfdianSyncDisabled && d.HasDuration && stringPtrValue(row.PlanName) == legacyOneTimePlanName {
		if d.LatestPlanName != "" {
			update.SetPlanName(d.LatestPlanName)
		} else {
			update.ClearPlanName()
		}
	}
	saved, err := update.Save(ctx)
	if postgresql.IsNotFound(err) {
		return nil, errSponsorUpdateConflict
	}
	return saved, err
}

// RecomputeAllSponsors recomputes every split row.
func RecomputeAllSponsors(ctx context.Context, db *postgresql.Client, now time.Time) error {
	ids, err := db.Sponsor.Query().Where(sponsorSchema.DurationSplitAtNotNil()).IDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := RecomputeSponsor(ctx, db, id, now); err != nil {
			return err
		}
	}
	return nil
}

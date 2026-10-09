package sponsor

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	sponsorSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsor"
	manualSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsormanualduration"
)

// Legacy duration split.
//
// Before Afdian and manual time were separate, each sponsor had one
// plan_expires_at that the Afdian sync, the webhook and the admin editor all
// wrote. SplitLegacySponsorDurations turns such a row (duration_split_at IS
// NULL) into the new model once:
//
//  1. recompute the Afdian time from the stored orders (and the expiry Afdian
//     reported, see ComputeAfdianPeriod), merged with any manual entries;
//  2. if the legacy expiry is later than that, add one manual entry
//     ("迁移自旧版手动调整") that ends at or after the legacy expiry, so no
//     effective expiry is ever shortened; it starts where the recomputed time
//     ends, or, without any, at the legacy paid_at / created_at;
//  3. set duration_split_at.
//
// The split is idempotent: rows are claimed with duration_split_at IS NULL in
// the same transaction that adds the entry. Afdian rows are only split once
// the order history is known to be complete (a full query-order pass, or no
// API credentials at all, when webhooks are the only history), otherwise the
// Afdian time would be mistaken for manual time.

const (
	// MigratedManualNote is the note of entries created by the split.
	MigratedManualNote = "迁移自旧版手动调整"
	migrationActor     = "system:migration"
)

// SplitOptions controls SplitLegacySponsorDurations.
type SplitOptions struct {
	// OrdersComplete says the stored Afdian orders are the full history.
	OrdersComplete bool
	// DryRun computes the report without writing.
	DryRun bool
}

// DurationSplitCase is a row whose recomputed duration was shorter than its
// legacy expiry and therefore got a migrated manual entry.
type DurationSplitCase struct {
	SponsorID           string     `json:"sponsorId"`
	LegacyExpiresAt     time.Time  `json:"legacyExpiresAt"`
	RecomputedExpiresAt *time.Time `json:"recomputedExpiresAt,omitzero"`
	MigratedDays        int        `json:"migratedDays"`
	// UnderOneDay marks a gap below one day: the legacy expiry used
	// calendar months where Afdian counts 31 days and midnight UTC+8.
	UnderOneDay bool `json:"underOneDay"`
}

// DurationSplitReport describes one split pass (or its dry run).
type DurationSplitReport struct {
	Rows                     int                 `json:"rows"`
	AlreadySplit             int                 `json:"alreadySplit"`
	Split                    int                 `json:"split"`
	SkippedIncompleteHistory int                 `json:"skippedIncompleteHistory"`
	MigratedEntries          int                 `json:"migratedEntries"`
	Cases                    []DurationSplitCase `json:"cases"`
	Categories               map[Category]int    `json:"categories"`
	LegacyCategories         map[Category]int    `json:"legacyCategories"`
	// MislabeledOneTime counts rows stored with the plan name "一次性赞助"
	// although they have duration: the sponsors requirement 2 was about.
	MislabeledOneTime int `json:"mislabeledOneTime"`
}

// String renders the report for the dry-run test and logs.
func (r DurationSplitReport) String() string {
	return fmt.Sprintf(
		"rows=%d already_split=%d split=%d skipped_incomplete_history=%d migrated_entries=%d (under_one_day=%d) mislabeled_one_time=%d\n  before: current=%d former=%d one_time=%d\n  after:  current=%d former=%d one_time=%d",
		r.Rows, r.AlreadySplit, r.Split, r.SkippedIncompleteHistory, r.MigratedEntries, r.underOneDay(), r.MislabeledOneTime,
		r.LegacyCategories[CategoryCurrent], r.LegacyCategories[CategoryFormer], r.LegacyCategories[CategoryOneTime],
		r.Categories[CategoryCurrent], r.Categories[CategoryFormer], r.Categories[CategoryOneTime],
	)
}

func (r DurationSplitReport) underOneDay() int {
	n := 0
	for _, c := range r.Cases {
		if c.UnderOneDay {
			n++
		}
	}
	return n
}

// legacyWallCategory is how the pre-split public wall sorted a row: a stored
// "一次性赞助"/"自选方案" plan name meant one-time, a manual source or an
// expired/missing expiry meant the past list, the rest was the duration list.
func legacyWallCategory(row *postgresql.Sponsor, now time.Time) Category {
	planName := stringPtrValue(row.PlanName)
	if planName == oneTimePlanName || planName == customPlanName {
		return CategoryOneTime
	}
	switch row.Source {
	case sponsorSchema.SourceManual, sponsorSchema.SourceLegacy, sponsorSchema.SourceImported:
		return CategoryFormer
	}
	if row.PlanExpiresAt == nil || !row.PlanExpiresAt.After(now) {
		return CategoryFormer
	}
	return CategoryCurrent
}

// SplitLegacySponsorDurations splits every unsplit row; see the comment at
// the top of this file.
func SplitLegacySponsorDurations(ctx context.Context, db *postgresql.Client, now time.Time, opts SplitOptions) (DurationSplitReport, error) {
	report := DurationSplitReport{Categories: map[Category]int{}, LegacyCategories: map[Category]int{}}
	rows, err := db.Sponsor.Query().Order(sponsorSchema.ByID()).All(ctx)
	if err != nil {
		return report, err
	}
	report.Rows = len(rows)
	for _, row := range rows {
		if row.DurationSplitAt != nil {
			report.AlreadySplit++
			report.Categories[SponsorCategory(row, now)]++
			continue
		}
		report.LegacyCategories[legacyWallCategory(row, now)]++
		if row.AfdianUserID != nil && !opts.OrdersComplete {
			report.SkippedIncompleteHistory++
			report.Categories[SponsorCategory(row, now)]++
			continue
		}

		durations, err := LoadSponsorDurations(ctx, db, row)
		if err != nil {
			return report, err
		}
		if durations.HasDuration && stringPtrValue(row.PlanName) == oneTimePlanName {
			report.MislabeledOneTime++
		}
		entry, splitCase := planMigratedEntry(row, durations.EffectiveExpiresAt)
		if entry != nil {
			report.MigratedEntries++
			report.Cases = append(report.Cases, splitCase)
			durations.Manual = append(durations.Manual, *entry)
			durations.EffectiveExpiresAt = ComputeEffectiveExpiry(durations.Afdian.End, durations.Manual)
			durations.HasDuration = true
		}
		report.Categories[CategoryFor(durations.HasDuration, durations.EffectiveExpiresAt, now)]++
		if opts.DryRun {
			report.Split++
			continue
		}
		claimed, err := applySplit(ctx, db, row.ID, entry, now)
		if err != nil {
			return report, err
		}
		if !claimed {
			continue
		}
		report.Split++
		if _, err := RecomputeSponsor(ctx, db, row.ID, now); err != nil {
			return report, err
		}
	}
	sort.Slice(report.Cases, func(i, j int) bool { return report.Cases[i].SponsorID < report.Cases[j].SponsorID })
	return report, nil
}

// planMigratedEntry returns the manual entry that keeps the legacy expiry, or
// nil when the recomputed time already reaches it.
func planMigratedEntry(row *postgresql.Sponsor, recomputed *time.Time) (*ManualDurationFacts, DurationSplitCase) {
	legacy := row.PlanExpiresAt
	if legacy == nil || (recomputed != nil && !legacy.After(*recomputed)) {
		return nil, DurationSplitCase{}
	}
	var start time.Time
	switch {
	case recomputed != nil:
		start = *recomputed
	case row.PaidAt != nil && row.PaidAt.Before(*legacy):
		start = *row.PaidAt
	case row.CreatedAt.Before(*legacy):
		start = row.CreatedAt
	default:
		start = legacy.Add(-24 * time.Hour)
	}
	gap := legacy.Sub(start)
	days := int(gap / (24 * time.Hour))
	if gap%(24*time.Hour) != 0 {
		days++
	}
	entry := &ManualDurationFacts{Amount: days, Unit: ManualUnitDay, StartsAt: start.UTC()}
	return entry, DurationSplitCase{
		SponsorID:           row.ID,
		LegacyExpiresAt:     legacy.UTC(),
		RecomputedExpiresAt: recomputed,
		MigratedDays:        days,
		UnderOneDay:         recomputed != nil && gap < 24*time.Hour,
	}
}

// applySplit claims the row and writes the migrated entry in one transaction.
// It returns false when another process split the row first.
func applySplit(ctx context.Context, db *postgresql.Client, sponsorID string, entry *ManualDurationFacts, now time.Time) (bool, error) {
	tx, err := db.Tx(ctx)
	if err != nil {
		return false, err
	}
	claimed, err := tx.Sponsor.Update().
		Where(sponsorSchema.IDEQ(sponsorID), sponsorSchema.DurationSplitAtIsNil()).
		SetDurationSplitAt(now).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return false, err
	}
	if claimed == 0 {
		_ = tx.Rollback()
		return false, nil
	}
	if entry != nil {
		err = tx.SponsorManualDuration.Create().
			SetSponsorID(sponsorID).
			SetAmount(entry.Amount).
			SetUnit(manualSchema.Unit(entry.Unit)).
			SetStartsAt(entry.StartsAt).
			SetNote(MigratedManualNote).
			SetOrigin(manualSchema.OriginMigration).
			SetCreatedBy(migrationActor).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			Exec(ctx)
		if err != nil {
			_ = tx.Rollback()
			return false, err
		}
	}
	return true, tx.Commit()
}

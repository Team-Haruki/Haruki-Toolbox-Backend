package sponsor

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	manualSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/sponsormanualduration"
)

const (
	manualNoteMaxLen   = 500
	manualMaxDays      = 36600
	manualMaxMonths    = 1200
	manualActorMaxLen  = 128
	manualStartsAtSkew = 100 * 365 * 24 * time.Hour
)

var (
	// ErrSponsorNotSplit rejects manual entries on a row that still carries
	// the legacy single expiry; the split must run first.
	ErrSponsorNotSplit = errors.New("sponsor duration migration has not run for this sponsor yet")
	// ErrManualDurationNotFound is returned for an entry id that does not
	// belong to the sponsor.
	ErrManualDurationNotFound = errors.New("manual duration entry not found")
	// ErrInvalidManualDuration is returned for an out-of-range entry.
	ErrInvalidManualDuration = errors.New("invalid manual duration entry")
)

// ManualDurationInput is an admin's entry. Nil fields of an update are kept.
type ManualDurationInput struct {
	Amount   *int
	Unit     *string
	StartsAt *time.Time
	Note     *string
}

func validateManualAmount(amount int, unit ManualUnit) error {
	switch unit {
	case ManualUnitDay:
		if amount < 1 || amount > manualMaxDays {
			return ErrInvalidManualDuration
		}
	case ManualUnitMonth:
		if amount < 1 || amount > manualMaxMonths {
			return ErrInvalidManualDuration
		}
	default:
		return ErrInvalidManualDuration
	}
	return nil
}

func normalizeManualNote(note string) (string, error) {
	note = strings.TrimSpace(note)
	if note == "" || len(note) > manualNoteMaxLen {
		return "", ErrInvalidManualDuration
	}
	return note, nil
}

func validateStartsAt(startsAt time.Time, now time.Time) error {
	if startsAt.Before(now.Add(-manualStartsAtSkew)) || startsAt.After(now.Add(manualStartsAtSkew)) {
		return ErrInvalidManualDuration
	}
	return nil
}

func loadSplitSponsor(ctx context.Context, db *postgresql.Client, sponsorID string) (*postgresql.Sponsor, error) {
	row, err := db.Sponsor.Get(ctx, sponsorID)
	if err != nil {
		return nil, err
	}
	if row.DurationSplitAt == nil {
		return nil, ErrSponsorNotSplit
	}
	return row, nil
}

// AddManualDuration records manual time for a sponsor and recomputes it.
// starts_at defaults to now.
func AddManualDuration(ctx context.Context, db *postgresql.Client, sponsorID string, input ManualDurationInput, actor string, now time.Time) (*postgresql.SponsorManualDuration, error) {
	if _, err := loadSplitSponsor(ctx, db, sponsorID); err != nil {
		return nil, err
	}
	if input.Amount == nil || input.Unit == nil || input.Note == nil {
		return nil, ErrInvalidManualDuration
	}
	unit := ManualUnit(strings.TrimSpace(*input.Unit))
	if err := validateManualAmount(*input.Amount, unit); err != nil {
		return nil, err
	}
	note, err := normalizeManualNote(*input.Note)
	if err != nil {
		return nil, err
	}
	startsAt := now
	if input.StartsAt != nil {
		startsAt = input.StartsAt.UTC()
	}
	if err := validateStartsAt(startsAt, now); err != nil {
		return nil, err
	}
	entry, err := db.SponsorManualDuration.Create().
		SetSponsorID(sponsorID).
		SetAmount(*input.Amount).
		SetUnit(manualSchema.Unit(unit)).
		SetStartsAt(startsAt).
		SetNote(note).
		SetOrigin(manualSchema.OriginAdmin).
		SetCreatedBy(trimLimit(actor, manualActorMaxLen)).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := RecomputeSponsor(ctx, db, sponsorID, now); err != nil {
		return nil, err
	}
	return entry, nil
}

func loadManualEntry(ctx context.Context, db *postgresql.Client, sponsorID string, entryID int) (*postgresql.SponsorManualDuration, error) {
	entry, err := db.SponsorManualDuration.Query().
		Where(manualSchema.IDEQ(entryID), manualSchema.SponsorIDEQ(sponsorID)).
		Only(ctx)
	if postgresql.IsNotFound(err) {
		return nil, ErrManualDurationNotFound
	}
	return entry, err
}

// UpdateManualDuration edits an entry (migrated ones included) and
// recomputes the sponsor.
func UpdateManualDuration(ctx context.Context, db *postgresql.Client, sponsorID string, entryID int, input ManualDurationInput, actor string, now time.Time) (*postgresql.SponsorManualDuration, error) {
	if _, err := loadSplitSponsor(ctx, db, sponsorID); err != nil {
		return nil, err
	}
	entry, err := loadManualEntry(ctx, db, sponsorID, entryID)
	if err != nil {
		return nil, err
	}
	amount := entry.Amount
	if input.Amount != nil {
		amount = *input.Amount
	}
	unit := ManualUnit(entry.Unit)
	if input.Unit != nil {
		unit = ManualUnit(strings.TrimSpace(*input.Unit))
	}
	if err := validateManualAmount(amount, unit); err != nil {
		return nil, err
	}
	update := entry.Update().
		SetAmount(amount).
		SetUnit(manualSchema.Unit(unit)).
		SetUpdatedBy(trimLimit(actor, manualActorMaxLen)).
		SetUpdatedAt(now)
	if input.Note != nil {
		note, err := normalizeManualNote(*input.Note)
		if err != nil {
			return nil, err
		}
		update.SetNote(note)
	}
	if input.StartsAt != nil {
		startsAt := input.StartsAt.UTC()
		if err := validateStartsAt(startsAt, now); err != nil {
			return nil, err
		}
		update.SetStartsAt(startsAt)
	}
	saved, err := update.Save(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := RecomputeSponsor(ctx, db, sponsorID, now); err != nil {
		return nil, err
	}
	return saved, nil
}

// DeleteManualDuration removes an entry and recomputes the sponsor.
func DeleteManualDuration(ctx context.Context, db *postgresql.Client, sponsorID string, entryID int, now time.Time) error {
	if _, err := loadSplitSponsor(ctx, db, sponsorID); err != nil {
		return err
	}
	if _, err := loadManualEntry(ctx, db, sponsorID, entryID); err != nil {
		return err
	}
	if err := db.SponsorManualDuration.DeleteOneID(entryID).Exec(ctx); err != nil {
		if postgresql.IsNotFound(err) {
			return ErrManualDurationNotFound
		}
		return err
	}
	_, err := RecomputeSponsor(ctx, db, sponsorID, now)
	return err
}

// ManualSponsorInput is the profile of a supporter entered by hand.
type ManualSponsorInput struct {
	Name     string
	Avatar   string
	PlanName string
	Message  string
}

// CreateManualSponsor adds a supporter who did not come through Afdian. It
// has no time until an admin adds manual entries.
func CreateManualSponsor(ctx context.Context, db *postgresql.Client, input ManualSponsorInput, now time.Time) (*postgresql.Sponsor, error) {
	return db.Sponsor.Create().
		SetID(NewManualSponsorID()).
		SetNillableName(stringPointerOrNil(trimLimit(input.Name, 128))).
		SetNillableAvatar(stringPointerOrNil(trimLimit(input.Avatar, 500))).
		SetNillablePlanName(stringPointerOrNil(trimLimit(input.PlanName, 128))).
		SetNillableMessage(stringPointerOrNil(trimLimit(input.Message, 1000))).
		SetSource("manual").
		SetIsActive(false).
		SetDurationSplitAt(now).
		SetSupportCount(0).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
}

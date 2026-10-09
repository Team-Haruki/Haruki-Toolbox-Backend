package bootstrap

import (
	"context"
	"fmt"

	db "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
)

// checkSponsorDurationSchemaSQL checks the tables and columns of the split
// sponsor duration model (Afdian orders, manual entries, cached columns).
// sponsors.afdian_reported_at is the last column of that change.
const checkSponsorDurationSchemaSQL = `
SELECT to_regclass('public.sponsor_afdian_orders') IS NOT NULL
	AND to_regclass('public.sponsor_manual_durations') IS NOT NULL
	AND (
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'sponsors'
			AND column_name IN ('afdian_expires_at', 'afdian_duration_months', 'afdian_reported_expires_at', 'afdian_reported_at', 'has_duration', 'duration_split_at')
	) = 6
`

const sponsorDurationMigrationHint = "apply the sponsor duration schema migration (see the operations docs) or enable backend.auto_migrate"

// validateSponsorDurationSchema runs after the optional Ent auto-migration,
// which creates everything it checks; with auto_migrate disabled the operator
// applies the DDL by hand.
func validateSponsorDurationSchema(ctx context.Context, client *db.Client) error {
	sqlDB := client.SQLDB()
	if sqlDB == nil {
		return fmt.Errorf("underlying SQL DB is unavailable")
	}
	var ok bool
	if err := sqlDB.QueryRowContext(ctx, checkSponsorDurationSchemaSQL).Scan(&ok); err != nil {
		return fmt.Errorf("inspect sponsor duration schema: %w", err)
	}
	if !ok {
		return fmt.Errorf("sponsor_afdian_orders, sponsor_manual_durations or the sponsors duration columns are missing; %s", sponsorDurationMigrationHint)
	}
	return nil
}

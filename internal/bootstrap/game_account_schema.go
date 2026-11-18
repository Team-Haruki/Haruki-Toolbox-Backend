package bootstrap

import (
	"context"
	"database/sql"
	"fmt"
)

const canonicalBindingConstraint = "game_account_bindings_canonical_game_user_id"

// An existing unique index on (server, game_user_id) also enforces numeric
// uniqueness once every stored game_user_id is in the canonical bigint form.
func ensureCanonicalGameAccountBindings(ctx context.Context, db *sql.DB, migrate bool) error {
	const hasConstraint = `SELECT EXISTS (SELECT 1 FROM pg_constraint
		WHERE conrelid = 'game_account_bindings'::regclass AND conname = $1 AND convalidated)`
	var installed bool
	if err := db.QueryRowContext(ctx, hasConstraint, canonicalBindingConstraint).Scan(&installed); err != nil || installed {
		return err
	}
	if !migrate {
		return fmt.Errorf("canonical game account constraint is missing; apply the game account identity migration or enable backend.auto_migrate")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `LOCK TABLE game_account_bindings, game_account_data_grants IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, hasConstraint, canonicalBindingConstraint).Scan(&installed); err != nil || installed {
		return err
	}
	var invalid, collisions int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM game_account_bindings
		WHERE NOT pg_input_is_valid(btrim(game_user_id), 'bigint')`).Scan(&invalid); err != nil {
		return err
	}
	if invalid != 0 {
		return fmt.Errorf("found %d game account bindings with invalid IDs; reconcile their ownership before migrating", invalid)
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM (
		SELECT server, btrim(game_user_id)::bigint FROM game_account_bindings
		GROUP BY server, btrim(game_user_id)::bigint HAVING count(*) > 1) conflicting_accounts`).Scan(&collisions); err != nil {
		return err
	}
	if collisions != 0 {
		return fmt.Errorf("found %d conflicting numeric game accounts; reconcile ownership and privacy settings before migrating", collisions)
	}
	// Move only grants already bound to this exact owner and old spelling. A
	// conflicting grant aborts the transaction rather than merging permissions.
	if _, err = tx.ExecContext(ctx, `UPDATE game_account_data_grants AS g
		SET game_user_id = btrim(b.game_user_id)::bigint::text
		FROM game_account_bindings AS b
		WHERE b.user_game_account_bindings = g.owner_user_id
		AND b.server = g.server AND b.game_user_id = g.game_user_id
		AND b.game_user_id <> btrim(b.game_user_id)::bigint::text`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE game_account_bindings SET game_user_id = btrim(game_user_id)::bigint::text
		WHERE game_user_id <> btrim(game_user_id)::bigint::text`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `ALTER TABLE game_account_bindings ADD CONSTRAINT game_account_bindings_canonical_game_user_id
		CHECK (CASE WHEN pg_input_is_valid(game_user_id, 'bigint') THEN game_user_id = game_user_id::bigint::text ELSE false END)`); err != nil {
		return err
	}
	return tx.Commit()
}

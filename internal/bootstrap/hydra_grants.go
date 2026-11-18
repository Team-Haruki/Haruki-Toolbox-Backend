package bootstrap

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func lockHydraClientGrants(db *sql.DB) func(context.Context, string, bool) (func(), error) {
	return func(ctx context.Context, clientID string, exclusive bool) (func(), error) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		query := `SELECT pg_advisory_xact_lock_shared(hashtextextended($1, 618703))`
		if exclusive {
			query = `SELECT pg_advisory_xact_lock(hashtextextended($1, 618703))`
		}
		waitCtx, cancelWait := context.WithTimeout(ctx, 3*time.Second)
		defer cancelWait()
		if _, err := tx.ExecContext(waitCtx, query, clientID); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		return func() { _ = tx.Rollback() }, nil
	}
}

// Hydra's admin token DELETE covers access tokens only. Both supported Hydra
// versions store refresh grants by client ID and network in this relation.
// Use a separate database role scoped to this operation.
func purgeHydraRefreshGrants(db *sql.DB) func(context.Context, string) error {
	return func(ctx context.Context, clientID string) error {
		if strings.TrimSpace(clientID) == "" {
			return fmt.Errorf("Hydra client ID is required")
		}
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		var network string
		if err := db.QueryRowContext(ctx, `SELECT (SELECT nid FROM hydra_client WHERE id = $1)::text`, clientID).Scan(&network); err != nil {
			return fmt.Errorf("resolve Hydra client network: %w", err)
		}
		_, err := db.ExecContext(ctx, `DELETE FROM hydra_oauth2_refresh WHERE client_id = $1 AND nid = $2::uuid`, clientID, network)
		return err
	}
}

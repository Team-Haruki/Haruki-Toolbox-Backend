package oauth2

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/pgadvisory"
)

// NewHydraClientGrantLocker serializes client lifecycle changes with token grants.
func NewHydraClientGrantLocker(db *sql.DB) func(context.Context, string, bool) (func(), error) {
	return func(ctx context.Context, clientID string, exclusive bool) (func(), error) {
		query := `SELECT pg_advisory_xact_lock_shared(hashtextextended($1, 618703))`
		if exclusive {
			query = `SELECT pg_advisory_xact_lock(hashtextextended($1, 618703))`
		}
		waitCtx, cancelWait := context.WithTimeout(ctx, 3*time.Second)
		defer cancelWait()
		return pgadvisory.Lock(waitCtx, db, query, clientID)
	}
}

// NewHydraRefreshGrantPurger deletes a client's refresh grants.
// Hydra's admin token DELETE covers access tokens only. Both supported Hydra
// versions store refresh grants by client ID and network in this relation.
// Use a separate database role scoped to this operation.
func NewHydraRefreshGrantPurger(db *sql.DB) func(context.Context, string) error {
	return func(ctx context.Context, clientID string) error {
		if strings.TrimSpace(clientID) == "" {
			return fmt.Errorf("missing Hydra client ID")
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

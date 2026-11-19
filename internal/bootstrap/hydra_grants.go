package bootstrap

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
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

func (application *Application) buildHydraProviderConfig(cfg harukiConfig.Config, toolboxDB *sql.DB) (*harukiOAuth2.HydraConfig, error) {
	var purgeRefreshGrants func(context.Context, string) error
	if cfg.OAuth2.HydraGrantStoreURL != "" {
		grantDB, err := openTunedSQLDB("postgres", cfg.OAuth2.HydraGrantStoreURL, 4, 1)
		if err != nil {
			return nil, fmt.Errorf("initialize Hydra refresh grant store")
		}
		application.addResourceCloser("Hydra refresh grant store", grantDB.Close)
		purgeRefreshGrants = purgeHydraRefreshGrants(grantDB)
	}
	return harukiOAuth2.NewHydraConfig(harukiOAuth2.HydraConfigOptions{
		Provider:           cfg.OAuth2.Provider,
		PublicURL:          cfg.OAuth2.HydraPublicURL,
		BrowserURL:         cfg.OAuth2.HydraBrowserURL,
		AdminURL:           cfg.OAuth2.HydraAdminURL,
		ResourceAudience:   cfg.Backend.BackendURL,
		ClientID:           cfg.OAuth2.HydraClientID,
		ClientSecret:       cfg.OAuth2.HydraClientSecret,
		RequestTimeout:     time.Duration(cfg.OAuth2.HydraRequestTimeoutSecond) * time.Second,
		PurgeRefreshGrants: purgeRefreshGrants,
		LockClientGrants:   lockHydraClientGrants(toolboxDB),
	}), nil
}

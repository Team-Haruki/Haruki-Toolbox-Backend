package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"

	"github.com/lib/pq"
)

func (application *Application) buildHydraProviderConfig(cfg harukiConfig.Config, grantLocks *sql.DB) (*oauth2.HydraConfig, error) {
	var purgeRefreshGrants func(context.Context, string) error
	var consentSubjects func(context.Context, string, int) ([]string, error)
	if cfg.OAuth2.HydraGrantStoreURL != "" {
		grantDB, err := openTunedSQLDB("postgres", cfg.OAuth2.HydraGrantStoreURL, 4, 1)
		if err != nil {
			return nil, fmt.Errorf("initialize Hydra refresh grant store")
		}
		application.addResourceCloser("Hydra refresh grant store", grantDB.Close)
		purgeRefreshGrants = oauth2.NewHydraRefreshGrantPurger(grantDB)
		consentSubjects = hydraConsentSubjects(grantDB)
	}
	return oauth2.NewHydraConfig(oauth2.HydraConfigOptions{
		Provider:           cfg.OAuth2.Provider,
		PublicURL:          cfg.OAuth2.HydraPublicURL,
		BrowserURL:         cfg.OAuth2.HydraBrowserURL,
		AdminURL:           cfg.OAuth2.HydraAdminURL,
		ResourceAudience:   cfg.Backend.BackendURL,
		ClientID:           cfg.OAuth2.HydraClientID,
		ClientSecret:       cfg.OAuth2.HydraClientSecret,
		RequestTimeout:     time.Duration(cfg.OAuth2.HydraRequestTimeoutSecond) * time.Second,
		PurgeRefreshGrants: purgeRefreshGrants,
		ConsentSubjects:    consentSubjects,
		LockClientGrants:   oauth2.NewHydraClientGrantLocker(grantLocks),
	}), nil
}

// hydraConsentSubjects identifies candidates by client and network. Hydra's
// Admin API still decides which consent sessions are current for each subject.
func hydraConsentSubjects(db *sql.DB) func(context.Context, string, int) ([]string, error) {
	return func(ctx context.Context, clientID string, limit int) ([]string, error) {
		const query = `
SELECT DISTINCT flow.subject
FROM hydra_oauth2_flow AS flow
WHERE flow.client_id = $1
  AND flow.nid = (SELECT nid FROM hydra_client WHERE id = $1)
  AND flow.subject <> ''
  AND (flow.expires_at IS NULL OR flow.expires_at > CURRENT_TIMESTAMP)
ORDER BY flow.subject
LIMIT $2`
		rows, err := db.QueryContext(ctx, query, clientID, limit)
		if err != nil {
			var pgErr *pq.Error
			if errors.As(err, &pgErr) && pgErr.Code == "42501" {
				return nil, oauth2.ErrConsentIndexUnavailable
			}
			return nil, err
		}
		defer rows.Close()
		subjects := make([]string, 0)
		for rows.Next() {
			var subject string
			if err := rows.Scan(&subject); err != nil {
				return nil, err
			}
			subjects = append(subjects, subject)
		}
		return subjects, rows.Err()
	}
}

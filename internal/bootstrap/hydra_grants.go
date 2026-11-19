package bootstrap

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiOAuth2 "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/oauth2"
)

func (application *Application) buildHydraProviderConfig(cfg harukiConfig.Config, toolboxDB *sql.DB) (*harukiOAuth2.HydraConfig, error) {
	var purgeRefreshGrants func(context.Context, string) error
	if cfg.OAuth2.HydraGrantStoreURL != "" {
		grantDB, err := openTunedSQLDB("postgres", cfg.OAuth2.HydraGrantStoreURL, 4, 1)
		if err != nil {
			return nil, fmt.Errorf("initialize Hydra refresh grant store")
		}
		application.addResourceCloser("Hydra refresh grant store", grantDB.Close)
		purgeRefreshGrants = harukiOAuth2.NewHydraRefreshGrantPurger(grantDB)
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
		LockClientGrants:   harukiOAuth2.NewHydraClientGrantLocker(toolboxDB),
	}), nil
}

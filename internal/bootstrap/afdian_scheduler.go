package bootstrap

import (
	"context"
	"strings"
	"time"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	sponsorModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/sponsor"
	dbManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
)

const afdianSponsorSyncInterval = 5 * time.Minute

func startAfdianSponsorSyncScheduler(ctx context.Context, db *dbManager.Client, cfg harukiConfig.AfdianConfig, logger *harukiLogger.Logger) {
	if strings.TrimSpace(cfg.UserID) == "" || strings.TrimSpace(cfg.APIToken) == "" {
		logger.Infof("afdian sponsor sync scheduler disabled: afdian user_id or api token is not configured")
		return
	}

	logger.Infof("afdian sponsor sync scheduler enabled with interval %s", afdianSponsorSyncInterval)
	go func() {
		runAfdianSponsorSync(ctx, db, cfg, logger)

		ticker := time.NewTicker(afdianSponsorSyncInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				logger.Infof("afdian sponsor sync scheduler stopped")
				return
			case <-ticker.C:
				runAfdianSponsorSync(ctx, db, cfg, logger)
			}
		}
	}()
}

func runAfdianSponsorSync(ctx context.Context, db *dbManager.Client, cfg harukiConfig.AfdianConfig, logger *harukiLogger.Logger) {
	startedAt := time.Now().UTC()
	result, err := sponsorModule.SyncAfdianSponsors(ctx, db, cfg, startedAt)
	if err != nil {
		if ctx.Err() != nil {
			logger.Warnf("afdian sponsor sync canceled: %v", ctx.Err())
			return
		}
		logger.Warnf("afdian sponsor sync failed: %v", err)
		return
	}
	logger.Infof("afdian sponsor sync completed: imported=%d skipped=%d duration=%s", result.Imported, result.Skipped, time.Since(startedAt).Round(time.Millisecond))
}

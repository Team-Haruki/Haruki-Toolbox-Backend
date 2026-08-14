package bootstrap

import (
	"context"
	"sync"
	"time"

	sponsorModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/sponsor"
	dbManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
)

// startAfdianSponsorSyncScheduler launches the background sync and returns a
// function that blocks until the goroutine has fully exited. Callers must cancel
// ctx and then invoke the returned wait before closing the Ent client, otherwise
// an in-flight sync can use the client after it is closed.
func startAfdianSponsorSyncScheduler(ctx context.Context, db *dbManager.Client, cfg sponsorModule.AfdianConfig, logger *harukiLogger.Logger) func() {
	if !cfg.SyncEnabled() {
		logger.Infof("afdian sponsor sync scheduler disabled: sync_enabled is false")
		return func() {}
	}
	if !cfg.CredentialsConfigured() {
		logger.Infof("afdian sponsor sync scheduler disabled: afdian user_id or api token is not configured")
		return func() {}
	}

	interval := cfg.SyncInterval()

	logger.Infof("afdian sponsor sync scheduler enabled with interval %s", interval)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		runAfdianSponsorSync(ctx, db, cfg, logger)

		ticker := time.NewTicker(interval)
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
	return wg.Wait
}

func runAfdianSponsorSync(ctx context.Context, db *dbManager.Client, cfg sponsorModule.AfdianConfig, logger *harukiLogger.Logger) {
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

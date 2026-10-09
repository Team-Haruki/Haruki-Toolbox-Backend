package bootstrap

import (
	"context"
	"sync"
	"time"

	sponsorModule "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/modules/sponsor"
	dbManager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
)

// afdianFullSyncInterval is how often a periodic pass walks every order page.
const afdianFullSyncInterval = 24 * time.Hour

// startAfdianSponsorSyncScheduler launches the background sync and returns a
// function that blocks until the goroutine has fully exited. Callers must cancel
// ctx and then invoke the returned wait before closing the Ent client, otherwise
// an in-flight sync can use the client after it is closed.
//
// The first pass of every process fetches the whole order history, which also
// splits sponsors still carrying the legacy single expiry (see
// sponsorModule.SplitLegacySponsorDurations); later passes stop at the first
// page of already-known orders, with one full pass a day in case an older
// order was missed. With sync disabled the full pass still runs
// once while unsplit rows remain, and without API credentials the stored
// webhook orders are the only history, so the split runs on them.
func startAfdianSponsorSyncScheduler(ctx context.Context, db *dbManager.Client, cfg sponsorModule.AfdianConfig, logger *harukiLogger.Logger) func() {
	if !cfg.CredentialsConfigured() {
		logger.Infof("afdian sponsor sync scheduler disabled: afdian user_id or api token is not configured")
		splitWithoutAfdianAPI(ctx, db, logger)
		return func() {}
	}
	if !cfg.SyncEnabled() {
		logger.Infof("afdian sponsor sync scheduler disabled: sync_enabled is false")
		pending, err := sponsorModule.HasUnsplitSponsors(ctx, db)
		if err != nil {
			logger.Warnf("afdian sponsor duration split check failed: %v", err)
			return func() {}
		}
		if !pending {
			return func() {}
		}
		logger.Infof("afdian sponsor duration split pending: running one full sync")
	}

	interval := cfg.SyncInterval()
	periodic := cfg.SyncEnabled()

	if periodic {
		logger.Infof("afdian sponsor sync scheduler enabled with interval %s", interval)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		runAfdianSponsorSync(ctx, db, cfg, logger, sponsorModule.SyncOptions{Full: true})
		if !periodic {
			return
		}
		lastFull := time.Now()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				logger.Infof("afdian sponsor sync scheduler stopped")
				return
			case <-ticker.C:
				full := time.Since(lastFull) >= afdianFullSyncInterval
				if pending, err := sponsorModule.HasUnsplitSponsors(ctx, db); err == nil && pending {
					full = true
				}
				if full {
					lastFull = time.Now()
				}
				runAfdianSponsorSync(ctx, db, cfg, logger, sponsorModule.SyncOptions{Full: full})
			}
		}
	}()
	return wg.Wait
}

func runAfdianSponsorSync(ctx context.Context, db *dbManager.Client, cfg sponsorModule.AfdianConfig, logger *harukiLogger.Logger, opts sponsorModule.SyncOptions) {
	startedAt := time.Now().UTC()
	result, err := sponsorModule.SyncAfdianSponsors(ctx, db, cfg, startedAt, opts)
	if err != nil {
		if ctx.Err() != nil {
			logger.Warnf("afdian sponsor sync canceled: %v", ctx.Err())
			return
		}
		logger.Warnf("afdian sponsor sync failed: %v", err)
		return
	}
	logger.Infof("afdian sponsor sync completed: full=%t orders=%d new_orders=%d imported=%d skipped=%d split=%d duration=%s",
		result.Full, result.Orders, result.NewOrders, result.Imported, result.Skipped, result.Split, time.Since(startedAt).Round(time.Millisecond))
}

func splitWithoutAfdianAPI(ctx context.Context, db *dbManager.Client, logger *harukiLogger.Logger) {
	report, err := sponsorModule.SplitLegacySponsorDurations(ctx, db, time.Now().UTC(), sponsorModule.SplitOptions{OrdersComplete: true})
	if err != nil {
		logger.Warnf("afdian sponsor duration split failed: %v", err)
		return
	}
	if report.Split > 0 {
		logger.Infof("afdian sponsor duration split (stored orders only): split=%d migrated_entries=%d", report.Split, report.MigratedEntries)
	}
}

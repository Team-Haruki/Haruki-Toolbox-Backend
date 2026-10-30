package inheritvault

import (
	"context"
	"sync"
	"time"

	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
)

// ReaperInterval is how often the reaper runs.
const ReaperInterval = 5 * time.Minute

// StartReaper runs Reap every interval until ctx ends, and once at start. The
// returned function waits for it to stop.
func StartReaper(ctx context.Context, store *Store, interval time.Duration, logger *harukiLogger.Logger) func() {
	if !store.Available() {
		return func() {}
	}
	if interval <= 0 {
		interval = ReaperInterval
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			runCtx, cancel := context.WithTimeout(ctx, time.Minute)
			res, err := store.Reap(runCtx)
			cancel()
			if err != nil && ctx.Err() == nil && logger != nil {
				logger.Warnf("Inherit vault reaper: %v", err)
			}
			if logger != nil && (res.Deleted > 0 || res.Reconciled > 0 || res.CodesCleared > 0) {
				logger.Infof("Inherit vault reaper: deleted=%d codes_cleared=%d reconciled=%d", res.Deleted, res.CodesCleared, res.Reconciled)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
	return wg.Wait
}

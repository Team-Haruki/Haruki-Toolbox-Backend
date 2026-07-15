package bootstrap

import (
	"context"
	"database/sql"
	"runtime"
	"sync"
	"time"

	harukiMongo "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/mongo"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
)

const defaultProfilingInterval = 15 * time.Second

// sqlPoolSource pairs a labeled name with a *sql.DB so the sampler can log each
// pool's database/sql stats. The Ent client does not re-expose the underlying
// *sql.DB, so bootstrap retains the handles returned by openTunedSQLDB.
type sqlPoolSource struct {
	name string
	db   *sql.DB
}

// startStatsSampler launches a background goroutine that periodically logs Mongo
// connection-pool telemetry, database/sql pool stats, and Go runtime/GC stats. It
// mirrors the afdian scheduler's lifecycle: cancel ctx then call the returned wait
// before closing the DB handles it samples, so it never touches a closed pool.
// poolStats may be nil (Mongo telemetry omitted). It is only started when profiling
// is enabled.
func startStatsSampler(ctx context.Context, interval time.Duration, poolStats *harukiMongo.PoolStats, sqlPools []sqlPoolSource, logger *harukiLogger.Logger) func() {
	if interval <= 0 {
		interval = defaultProfilingInterval
	}
	logger.Infof("profiling stats sampler enabled with interval %s", interval)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		var mem runtime.MemStats
		var lastNumGC uint32
		var lastPauseTotal uint64
		for {
			select {
			case <-ctx.Done():
				logger.Infof("profiling stats sampler stopped")
				return
			case <-ticker.C:
				sampleStats(poolStats, sqlPools, logger, &mem, &lastNumGC, &lastPauseTotal)
			}
		}
	}()
	return wg.Wait
}

func sampleStats(poolStats *harukiMongo.PoolStats, sqlPools []sqlPoolSource, logger *harukiLogger.Logger, mem *runtime.MemStats, lastNumGC *uint32, lastPauseTotal *uint64) {
	if poolStats != nil {
		s := poolStats.Snapshot()
		logger.Infof("mongo pool: checkedOut=%d pending=%d checkouts=%d failures=%d meanWait=%s maxWait=%s created=%d closed=%d",
			s.CheckedOut, s.Pending, s.Checkouts, s.CheckoutFailures,
			s.MeanWait.Round(time.Microsecond), s.MaxWait.Round(time.Microsecond), s.Created, s.Closed)
	}

	for _, p := range sqlPools {
		if p.db == nil {
			continue
		}
		st := p.db.Stats()
		logger.Infof("pg pool[%s]: open=%d/%d inUse=%d idle=%d waitCount=%d waitDuration=%s maxIdleClosed=%d maxLifetimeClosed=%d",
			p.name, st.OpenConnections, st.MaxOpenConnections, st.InUse, st.Idle,
			st.WaitCount, st.WaitDuration.Round(time.Millisecond), st.MaxIdleTimeClosed, st.MaxLifetimeClosed)
	}

	runtime.ReadMemStats(mem)
	gcDelta := mem.NumGC - *lastNumGC
	pauseDelta := mem.PauseTotalNs - *lastPauseTotal
	*lastNumGC = mem.NumGC
	*lastPauseTotal = mem.PauseTotalNs
	var meanPause time.Duration
	if gcDelta > 0 {
		meanPause = time.Duration(pauseDelta / uint64(gcDelta))
	}
	logger.Infof("runtime: goroutines=%d heapInUse=%dMiB heapObjects=%d gcCycles=%d meanGCPause=%s nextGC=%dMiB",
		runtime.NumGoroutine(), mem.HeapInuse/(1024*1024), mem.HeapObjects,
		gcDelta, meanPause.Round(time.Microsecond), mem.NextGC/(1024*1024))
}

package manager

import (
	"sync/atomic"
	"time"

	"go.mongodb.org/mongo-driver/v2/event"
)

// PoolStats accumulates MongoDB connection-pool telemetry from the driver's
// PoolMonitor event stream. Unlike database/sql the mongo driver exposes no Stats()
// snapshot, so pool health — the client-side checkout wait that is invisible to the
// server-side slow-query log — can only be observed by tallying pool events. The
// driver may invoke the monitor callback concurrently, so every field is atomic.
//
// Gauge fields (checkedOut, pending) reflect current state. The remaining fields are
// interval accumulators reset by Snapshot, so each sample reports what happened since
// the previous sample rather than an ever-growing total.
type PoolStats struct {
	checkedOut atomic.Int64 // connections currently checked out (in use)
	pending    atomic.Int64 // checkout requests started but not yet satisfied/failed

	created          atomic.Int64 // connections opened, since last snapshot
	closed           atomic.Int64 // connections closed, since last snapshot
	checkouts        atomic.Int64 // successful checkouts, since last snapshot
	checkoutFailures atomic.Int64 // failed checkouts, since last snapshot
	waitSamples      atomic.Int64 // checkout attempts (success+failure) that waited, since last snapshot
	waitNanosTotal   atomic.Int64 // summed checkout wait over waitSamples, since last snapshot
	waitNanosMax     atomic.Int64 // max single checkout wait, since last snapshot
}

// NewPoolStats returns a zero-valued collector ready to be attached via Monitor.
func NewPoolStats() *PoolStats {
	return &PoolStats{}
}

// Monitor returns an *event.PoolMonitor that feeds this collector. Pass it to
// WithPoolMonitor when constructing the manager.
func (s *PoolStats) Monitor() *event.PoolMonitor {
	return &event.PoolMonitor{Event: s.handle}
}

func (s *PoolStats) handle(evt *event.PoolEvent) {
	if s == nil || evt == nil {
		return
	}
	switch evt.Type {
	case event.ConnectionCreated:
		s.created.Add(1)
	case event.ConnectionClosed:
		s.closed.Add(1)
	case event.ConnectionCheckOutStarted:
		s.pending.Add(1)
	case event.ConnectionCheckedOut:
		s.pending.Add(-1)
		s.checkedOut.Add(1)
		s.checkouts.Add(1)
		s.waitSamples.Add(1)
		s.waitNanosTotal.Add(int64(evt.Duration))
		storeMaxNanos(&s.waitNanosMax, int64(evt.Duration))
	case event.ConnectionCheckOutFailed:
		// A checkout that waited then failed (e.g. pool exhaustion / selection
		// timeout) is the strongest saturation signal, so its wait counts toward the
		// mean/max just like a successful one.
		s.pending.Add(-1)
		s.checkoutFailures.Add(1)
		s.waitSamples.Add(1)
		s.waitNanosTotal.Add(int64(evt.Duration))
		storeMaxNanos(&s.waitNanosMax, int64(evt.Duration))
	case event.ConnectionCheckedIn:
		s.checkedOut.Add(-1)
	}
}

// PoolSnapshot is a point-in-time read of a PoolStats. Gauge fields are current;
// the rest cover the interval since the previous Snapshot call.
type PoolSnapshot struct {
	CheckedOut       int64
	Pending          int64
	Created          int64
	Closed           int64
	Checkouts        int64
	CheckoutFailures int64
	MeanWait         time.Duration
	MaxWait          time.Duration
}

// Snapshot reads the current gauges and drains the interval accumulators back to
// zero, so consecutive calls report per-interval deltas.
//
// This is a best-effort diagnostic, not an exact meter: the sample count and the
// summed wait are two independent atomics drained by separate Swap calls, so a
// checkout completing between them can have its wait counted in one interval and its
// sample in the next, transiently skewing MeanWait in a low-traffic window. The error
// self-corrects the following interval and is negligible over meaningful sample
// counts, so we accept it rather than lock the hot checkout path.
func (s *PoolStats) Snapshot() PoolSnapshot {
	samples := s.waitSamples.Swap(0)
	waitNanos := s.waitNanosTotal.Swap(0)
	var mean time.Duration
	if samples > 0 {
		mean = time.Duration(waitNanos / samples)
	}
	return PoolSnapshot{
		CheckedOut:       s.checkedOut.Load(),
		Pending:          s.pending.Load(),
		Created:          s.created.Swap(0),
		Closed:           s.closed.Swap(0),
		Checkouts:        s.checkouts.Swap(0),
		CheckoutFailures: s.checkoutFailures.Swap(0),
		MeanWait:         mean,
		MaxWait:          time.Duration(s.waitNanosMax.Swap(0)),
	}
}

// storeMaxNanos atomically raises a to v when v is larger, tolerating concurrent
// writers via compare-and-swap.
func storeMaxNanos(a *atomic.Int64, v int64) {
	for {
		old := a.Load()
		if v <= old {
			return
		}
		if a.CompareAndSwap(old, v) {
			return
		}
	}
}

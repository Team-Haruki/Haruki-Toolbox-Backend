package manager

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/event"
)

func TestPoolStatsSnapshot(t *testing.T) {
	s := NewPoolStats()
	m := s.Monitor()

	emit := func(typ string, d time.Duration) {
		m.Event(&event.PoolEvent{Type: typ, Duration: d})
	}

	// Two connections created, two checkouts (one slow), one still in use.
	emit(event.ConnectionCreated, 0)
	emit(event.ConnectionCreated, 0)
	emit(event.ConnectionCheckOutStarted, 0)
	emit(event.ConnectionCheckedOut, 2*time.Millisecond)
	emit(event.ConnectionCheckOutStarted, 0)
	emit(event.ConnectionCheckedOut, 8*time.Millisecond)
	emit(event.ConnectionCheckedIn, 0) // one returned; one still checked out
	emit(event.ConnectionCheckOutStarted, 0)
	emit(event.ConnectionCheckOutFailed, 5*time.Millisecond) // pending resolved as failure
	emit(event.ConnectionClosed, 0)

	snap := s.Snapshot()
	if snap.CheckedOut != 1 {
		t.Errorf("CheckedOut = %d, want 1", snap.CheckedOut)
	}
	if snap.Pending != 0 {
		t.Errorf("Pending = %d, want 0", snap.Pending)
	}
	if snap.Checkouts != 2 {
		t.Errorf("Checkouts = %d, want 2", snap.Checkouts)
	}
	if snap.CheckoutFailures != 1 {
		t.Errorf("CheckoutFailures = %d, want 1", snap.CheckoutFailures)
	}
	if snap.Created != 2 {
		t.Errorf("Created = %d, want 2", snap.Created)
	}
	if snap.Closed != 1 {
		t.Errorf("Closed = %d, want 1", snap.Closed)
	}
	if want := 5 * time.Millisecond; snap.MeanWait != want { // (2+8+5)/3, failure wait included
		t.Errorf("MeanWait = %s, want %s", snap.MeanWait, want)
	}
	if want := 8 * time.Millisecond; snap.MaxWait != want {
		t.Errorf("MaxWait = %s, want %s", snap.MaxWait, want)
	}

	// Interval accumulators reset; the gauge (CheckedOut) persists.
	snap2 := s.Snapshot()
	if snap2.Checkouts != 0 || snap2.CheckoutFailures != 0 || snap2.Created != 0 || snap2.Closed != 0 {
		t.Errorf("interval counters not reset: %+v", snap2)
	}
	if snap2.MaxWait != 0 || snap2.MeanWait != 0 {
		t.Errorf("wait accumulators not reset: mean=%s max=%s", snap2.MeanWait, snap2.MaxWait)
	}
	if snap2.CheckedOut != 1 {
		t.Errorf("CheckedOut gauge should persist, got %d", snap2.CheckedOut)
	}
}

func TestPoolStatsNilSafe(t *testing.T) {
	var s *PoolStats
	// handle must tolerate a nil receiver / nil event without panicking.
	s.Monitor().Event(nil)
	s2 := NewPoolStats()
	s2.handle(nil)
}

package circuitbreaker

import (
	"testing"
	"time"
)

const testServer = "jp"

const testRetryAfterFloor = time.Second

// newTestBreaker returns a breaker whose clock is driven by the returned pointer, so
// tests advance time deterministically.
func newTestBreaker(threshold int, window, cooldown time.Duration) (*Breaker[string], *time.Time) {
	b := New[string](threshold, window, cooldown, testRetryAfterFloor)
	now := time.Unix(1_700_000_000, 0)
	b.SetClock(func() time.Time { return now })
	return b, &now
}

// admit calls Allow, asserts it was permitted, and returns the epoch token the caller
// must feed back to RecordResult/ReleaseProbe.
func admit(t *testing.T, b *Breaker[string], server string) uint64 {
	t.Helper()
	allowed, _, token := b.Allow(server)
	if !allowed {
		t.Fatalf("expected Allow to permit request")
	}
	return token
}

func mustReject(t *testing.T, b *Breaker[string], server string) time.Duration {
	t.Helper()
	allowed, retryAfter, _ := b.Allow(server)
	if allowed {
		t.Fatalf("expected Allow to reject request")
	}
	if retryAfter < testRetryAfterFloor {
		t.Fatalf("retryAfter %s below floor %s", retryAfter, testRetryAfterFloor)
	}
	return retryAfter
}

func TestBreakerTripsAfterThreshold(t *testing.T) {
	b, _ := newTestBreaker(3, time.Minute, 30*time.Second)
	// Two degradations: still below threshold, still closed.
	for i := 0; i < 2; i++ {
		b.RecordResult(testServer, admit(t, b, testServer), true)
	}
	// Third degradation trips it.
	b.RecordResult(testServer, admit(t, b, testServer), true)
	mustReject(t, b, testServer)
}

func TestBreakerSuccessResetsFailures(t *testing.T) {
	b, _ := newTestBreaker(3, time.Minute, 30*time.Second)
	b.RecordResult(testServer, admit(t, b, testServer), true)
	b.RecordResult(testServer, admit(t, b, testServer), true)
	// A success clears accumulated failures, so two more degradations do not trip.
	b.RecordResult(testServer, admit(t, b, testServer), false)
	b.RecordResult(testServer, admit(t, b, testServer), true)
	b.RecordResult(testServer, admit(t, b, testServer), true)
	admit(t, b, testServer) // still closed
}

func TestBreakerWindowPrunesOldFailures(t *testing.T) {
	b, now := newTestBreaker(3, time.Minute, 30*time.Second)
	b.RecordResult(testServer, admit(t, b, testServer), true)
	b.RecordResult(testServer, admit(t, b, testServer), true)
	// Advance beyond the window so the earlier two failures no longer count.
	*now = now.Add(2 * time.Minute)
	b.RecordResult(testServer, admit(t, b, testServer), true)
	b.RecordResult(testServer, admit(t, b, testServer), true)
	// Only two failures fall within the window → still closed.
	admit(t, b, testServer)
	b.RecordResult(testServer, admit(t, b, testServer), true)
	// Now three within the window → tripped.
	mustReject(t, b, testServer)
}

func TestBreakerHalfOpenProbeSuccessCloses(t *testing.T) {
	b, now := newTestBreaker(1, time.Minute, 30*time.Second)
	b.RecordResult(testServer, admit(t, b, testServer), true) // trips (threshold 1)
	mustReject(t, b, testServer)
	// After cooldown a single probe is admitted.
	*now = now.Add(31 * time.Second)
	probe := admit(t, b, testServer)
	// A concurrent second request while the probe is in flight is rejected.
	mustReject(t, b, testServer)
	// Probe succeeds → closed, normal traffic resumes.
	b.RecordResult(testServer, probe, false)
	admit(t, b, testServer)
}

func TestBreakerHalfOpenProbeFailureReopens(t *testing.T) {
	b, now := newTestBreaker(1, time.Minute, 30*time.Second)
	b.RecordResult(testServer, admit(t, b, testServer), true)
	*now = now.Add(31 * time.Second)
	probe := admit(t, b, testServer) // probe admitted
	b.RecordResult(testServer, probe, true)
	// Probe failed → re-open, reject again until the next cooldown.
	mustReject(t, b, testServer)
	*now = now.Add(31 * time.Second)
	admit(t, b, testServer)
}

func TestBreakerReleaseProbeDoesNotClose(t *testing.T) {
	b, now := newTestBreaker(1, time.Minute, 30*time.Second)
	b.RecordResult(testServer, admit(t, b, testServer), true)
	*now = now.Add(31 * time.Second)
	probe := admit(t, b, testServer) // probe admitted, probePending=true
	// The caller could not exercise the upstream (local gate rejected it): release the
	// probe without a verdict. The breaker must NOT snap closed.
	b.ReleaseProbe(testServer, probe)
	// A subsequent request is admitted as the next probe (half-open), not as normal
	// closed traffic — crucially the breaker did not reset to closed on release.
	admit(t, b, testServer)
	// While that probe is in flight, another is rejected — proof we are still half-open.
	mustReject(t, b, testServer)
}

func TestBreakerIsolatesServers(t *testing.T) {
	b, _ := newTestBreaker(1, time.Minute, 30*time.Second)
	b.RecordResult(testServer, admit(t, b, testServer), true) // trip jp
	mustReject(t, b, testServer)
	// A different server is unaffected.
	admit(t, b, "en")
}

// TestBreakerStragglerSuccessDoesNotReopen is the regression guard for the finding
// that a straggler success re-closed an open breaker before its cooldown expired.
func TestBreakerStragglerSuccessDoesNotReopen(t *testing.T) {
	b, _ := newTestBreaker(3, time.Minute, 30*time.Second)
	// A slow request is admitted while closed and keeps running.
	straggler := admit(t, b, testServer)
	// Three other failures trip the breaker.
	for i := 0; i < 3; i++ {
		b.RecordResult(testServer, admit(t, b, testServer), true)
	}
	mustReject(t, b, testServer) // open
	// The straggler finally completes SUCCESSFULLY. It was admitted in the pre-trip
	// epoch, so it must NOT re-close the open breaker and bypass the cooldown.
	b.RecordResult(testServer, straggler, false)
	mustReject(t, b, testServer) // still open
}

// TestBreakerStragglerDoesNotResolveHalfOpenProbe guards the symmetric case: a
// straggler result arriving during half-open must not be mistaken for the probe.
func TestBreakerStragglerDoesNotResolveHalfOpenProbe(t *testing.T) {
	b, now := newTestBreaker(1, time.Minute, 30*time.Second)
	straggler := admit(t, b, testServer)                      // admitted while closed
	b.RecordResult(testServer, admit(t, b, testServer), true) // separate failure trips it
	mustReject(t, b, testServer)
	*now = now.Add(31 * time.Second)
	probe := admit(t, b, testServer) // half-open probe
	// The straggler completes with FAILURE during half-open; it must be ignored so the
	// real probe permit stays in flight.
	b.RecordResult(testServer, straggler, true)
	mustReject(t, b, testServer) // still half-open, probe pending → rejected
	// The real probe then succeeds → closes.
	b.RecordResult(testServer, probe, false)
	admit(t, b, testServer)
}

func TestNewClampsThreshold(t *testing.T) {
	b, _ := newTestBreaker(0, time.Minute, 30*time.Second)
	b.RecordResult(testServer, admit(t, b, testServer), true)
	mustReject(t, b, testServer)
}

func TestHalfOpenRejectsWithCooldownRetryAfter(t *testing.T) {
	b, now := newTestBreaker(1, time.Minute, 30*time.Second)
	b.RecordResult(testServer, admit(t, b, testServer), true)
	if got := mustReject(t, b, testServer); got != 30*time.Second {
		t.Fatalf("retryAfter right after trip = %s, want 30s", got)
	}
	*now = now.Add(31 * time.Second)
	admit(t, b, testServer)
	if got := mustReject(t, b, testServer); got != 30*time.Second {
		t.Fatalf("retryAfter while probing = %s, want the cooldown", got)
	}
}

func TestStaleReleaseProbeIsIgnored(t *testing.T) {
	b, now := newTestBreaker(1, time.Minute, 30*time.Second)
	stale := admit(t, b, testServer)
	b.RecordResult(testServer, admit(t, b, testServer), true)
	*now = now.Add(31 * time.Second)
	admit(t, b, testServer) // probe in flight
	b.ReleaseProbe(testServer, stale)
	mustReject(t, b, testServer) // the stale release did not free the probe slot
}

func TestRetryAfterSeconds(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want int
	}{{0, 1}, {-time.Second, 1}, {time.Millisecond, 1}, {time.Second, 1}, {1500 * time.Millisecond, 2}, {30 * time.Second, 30}} {
		if got := RetryAfterSeconds(tc.in); got != tc.want {
			t.Fatalf("RetryAfterSeconds(%s) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

package upload

import (
	"context"
	"errors"
	"testing"
	"time"

	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/sekai"
)

type timeoutError struct{}

func (timeoutError) Error() string { return "i/o timeout" }
func (timeoutError) Timeout() bool { return true }

func TestInheritFailureIsUpstreamDegradation(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil is not degradation", nil, false},
		{"deadline exceeded", context.DeadlineExceeded, true},
		{"deadline wrapped in retrieval error", harukiSekai.NewDataRetrievalError("run", "init", "client init failed", context.DeadlineExceeded), true},
		{"canceled", context.Canceled, true},
		{"maintenance", harukiSekai.ErrMaintenance, true},
		{"maintenance wrapped", harukiSekai.NewDataRetrievalError("mysekai", "check", "maintenance", harukiSekai.ErrMaintenance), true},
		{"api 500", harukiSekai.NewAPIError("/suite", "GET", 500, "boom", nil), true},
		{"api 503 wrapped", harukiSekai.NewDataRetrievalError("suite", "fetch", "unavailable", harukiSekai.NewAPIError("/suite", "GET", 503, "unavailable", nil)), true},
		{"api 429", harukiSekai.NewAPIError("/user", "PUT", 429, "slow down", nil), true},
		{"api 403 is user error", harukiSekai.NewAPIError("/inherit", "POST", 403, "forbidden", nil), false},
		{"api 400 wrapped is user error", harukiSekai.NewDataRetrievalError("run", "init", "bad creds", harukiSekai.NewAPIError("/inherit", "POST", 400, "bad", nil)), false},
		{"api 404 is user error", harukiSekai.NewAPIError("/user", "GET", 404, "missing", nil), false},
		{"net timeout without status", timeoutError{}, true},
		{"api zero-status wrapping timeout", harukiSekai.NewAPIError("/user", "GET", 0, "conn", timeoutError{}), true},
		// StatusCode 0 == transport failure (no HTTP response): a hard-down upstream
		// like connection-refused is degradation and must trip the breaker.
		{"api zero-status transport failure", harukiSekai.NewAPIError("/user", "GET", 0, "conn", errors.New("connection refused")), true},
		{"plain error is not degradation", errors.New("some parse error"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := inheritFailureIsUpstreamDegradation(tc.err); got != tc.want {
				t.Fatalf("inheritFailureIsUpstreamDegradation(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

const testServer = harukiUtils.SupportedInheritUploadServer("jp")

// newTestBreaker returns a breaker whose clock is driven by the returned pointer, so
// tests advance time deterministically.
func newTestBreaker(threshold int, window, cooldown time.Duration) (*inheritCircuitBreaker, *time.Time) {
	b := newInheritCircuitBreaker(threshold, window, cooldown)
	now := time.Unix(1_700_000_000, 0)
	b.now = func() time.Time { return now }
	return b, &now
}

// admit calls Allow, asserts it was permitted, and returns the epoch token the caller
// must feed back to RecordResult/ReleaseProbe.
func admit(t *testing.T, b *inheritCircuitBreaker, server harukiUtils.SupportedInheritUploadServer) uint64 {
	t.Helper()
	allowed, _, token := b.Allow(server)
	if !allowed {
		t.Fatalf("expected Allow to permit request")
	}
	return token
}

func mustReject(t *testing.T, b *inheritCircuitBreaker, server harukiUtils.SupportedInheritUploadServer) time.Duration {
	t.Helper()
	allowed, retryAfter, _ := b.Allow(server)
	if allowed {
		t.Fatalf("expected Allow to reject request")
	}
	if retryAfter < inheritBreakerRetryAfterFloor {
		t.Fatalf("retryAfter %s below floor %s", retryAfter, inheritBreakerRetryAfterFloor)
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
	admit(t, b, harukiUtils.SupportedInheritUploadServer("en"))
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

func TestConcurrencyLimiterBoundsPerServer(t *testing.T) {
	l := newInheritConcurrencyLimiter(2)
	if !l.acquire(testServer) {
		t.Fatal("first acquire should succeed")
	}
	if !l.acquire(testServer) {
		t.Fatal("second acquire should succeed")
	}
	if l.acquire(testServer) {
		t.Fatal("third acquire should fail (capacity 2)")
	}
	// A different server has its own slots.
	if !l.acquire(harukiUtils.SupportedInheritUploadServer("en")) {
		t.Fatal("other server acquire should succeed")
	}
	// Releasing frees a slot.
	l.release(testServer)
	if !l.acquire(testServer) {
		t.Fatal("acquire after release should succeed")
	}
}

func TestConcurrencyLimiterReleaseWithoutHoldIsSafe(t *testing.T) {
	l := newInheritConcurrencyLimiter(1)
	// Releasing when nothing is held must not panic or over-fill.
	l.release(testServer)
	if !l.acquire(testServer) {
		t.Fatal("acquire should succeed after spurious release")
	}
	if l.acquire(testServer) {
		t.Fatal("capacity must remain 1 after spurious release")
	}
}

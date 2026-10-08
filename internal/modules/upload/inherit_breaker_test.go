package upload

import (
	"context"
	"errors"
	"testing"
	"time"

	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/sekai"
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

// The breaker's state machine is tested in utils/circuitbreaker; this checks the
// inherit wiring: per-server keys and the Retry-After floor.
func TestInheritBreakerWiring(t *testing.T) {
	b := newInheritCircuitBreaker(1, time.Minute, 30*time.Second)
	now := time.Unix(1_700_000_000, 0)
	b.SetClock(func() time.Time { return now })
	allowed, _, token := b.Allow(testServer)
	if !allowed {
		t.Fatal("closed breaker rejected a request")
	}
	b.RecordResult(testServer, token, true)
	now = now.Add(30*time.Second - 100*time.Millisecond)
	allowed, retryAfter, _ := b.Allow(testServer)
	if allowed {
		t.Fatal("tripped breaker admitted a request")
	}
	if retryAfter != inheritBreakerRetryAfterFloor {
		t.Fatalf("retryAfter = %s, want the %s floor", retryAfter, inheritBreakerRetryAfterFloor)
	}
	if got := retryAfterSeconds(retryAfter); got != 1 {
		t.Fatalf("retryAfterSeconds = %d, want 1", got)
	}
	if allowed, _, _ := b.Allow(harukiUtils.SupportedInheritUploadServer("en")); !allowed {
		t.Fatal("a tripped server must not affect another server")
	}
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

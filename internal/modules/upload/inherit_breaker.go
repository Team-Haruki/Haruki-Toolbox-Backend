package upload

import (
	"context"
	"errors"
	"sync"
	"time"

	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/circuitbreaker"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/sekai"
)

// Inherit runs are slow (~30-90s) and hammer a single game server sequentially. Two
// per-server gates protect both the backend and the upstream game API without
// changing the synchronous client contract:
//
//   - a bounded concurrency limiter caps how many inherits run against one server at
//     once, fast-failing the overflow with 429 instead of piling up slow goroutines;
//   - a circuit breaker trips when the game API for a server is degraded (timeouts /
//     5xx / 429 / maintenance), fast-failing new inherits with 503 for a cooldown so
//     we stop hammering a server that is already failing.
//
// Both are keyed per server so a degraded JP does not starve or trip EN/TW/KR/CN.
const (
	inheritMaxConcurrentPerServer  = 8
	inheritBreakerFailureThreshold = 5
	inheritBreakerWindow           = 60 * time.Second
	inheritBreakerCooldown         = 30 * time.Second
	// inheritBreakerRetryAfterFloor is the minimum Retry-After advertised while open.
	inheritBreakerRetryAfterFloor = 1 * time.Second
)

// inheritFailureIsUpstreamDegradation reports whether an inherit failure signals a
// degraded/unavailable game API (which should trip the breaker) as opposed to a user
// or client error (bad inherit credentials, incomplete tutorial, not found), which
// means the upstream answered fine and must NOT trip it.
func inheritFailureIsUpstreamDegradation(err error) bool {
	if err == nil {
		return false
	}
	// Timeout/cancellation from a per-call (15s) or the whole-run (90s) deadline: the
	// upstream hung. This is the dominant degradation signal seen in production
	// (context deadline exceeded).
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	// Explicit maintenance means the feature/server is unavailable upstream.
	if harukiSekai.IsMaintenanceError(err) {
		return true
	}
	// Game-API 5xx and 429 are degradation; 4xx (bad credentials, forbidden, not
	// found) are user/client errors and must not trip the breaker. StatusCode 0 means
	// the request never received an HTTP response — a transport-level failure
	// (connection refused, TLS handshake error, reset, DNS failure, EOF), which the
	// callAPI helper reports as APIError{StatusCode: 0}; a hard-down upstream is
	// degradation.
	var apiErr *harukiSekai.APIError
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == 0 || apiErr.StatusCode >= 500 || apiErr.StatusCode == 429 {
			return true
		}
		if apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			return false
		}
	}
	// A transport-level timeout that did not surface as context.DeadlineExceeded.
	var timeoutErr interface{ Timeout() bool }
	if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
		return true
	}
	return false
}

// inheritConcurrencyLimiter bounds concurrent inherits per server with a
// non-blocking acquire: when a server's slots are full the caller is rejected
// immediately rather than queued.
type inheritConcurrencyLimiter struct {
	mu       sync.Mutex
	capacity int
	servers  map[harukiUtils.SupportedInheritUploadServer]chan struct{}
}

func newInheritConcurrencyLimiter(capacity int) *inheritConcurrencyLimiter {
	if capacity <= 0 {
		capacity = 1
	}
	return &inheritConcurrencyLimiter{
		capacity: capacity,
		servers:  make(map[harukiUtils.SupportedInheritUploadServer]chan struct{}),
	}
}

func (l *inheritConcurrencyLimiter) slots(server harukiUtils.SupportedInheritUploadServer) chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	ch, ok := l.servers[server]
	if !ok {
		ch = make(chan struct{}, l.capacity)
		l.servers[server] = ch
	}
	return ch
}

// acquire takes a slot for server without blocking, returning false when full.
func (l *inheritConcurrencyLimiter) acquire(server harukiUtils.SupportedInheritUploadServer) bool {
	select {
	case l.slots(server) <- struct{}{}:
		return true
	default:
		return false
	}
}

// release returns a previously acquired slot. It is a no-op if none is held.
func (l *inheritConcurrencyLimiter) release(server harukiUtils.SupportedInheritUploadServer) {
	select {
	case <-l.slots(server):
	default:
	}
}

// inheritCircuitBreaker is the shared per-key breaker (utils/circuitbreaker) keyed by
// inherit server.
type inheritCircuitBreaker = circuitbreaker.Breaker[harukiUtils.SupportedInheritUploadServer]

func newInheritCircuitBreaker(threshold int, window, cooldown time.Duration) *inheritCircuitBreaker {
	return circuitbreaker.New[harukiUtils.SupportedInheritUploadServer](threshold, window, cooldown, inheritBreakerRetryAfterFloor)
}

// retryAfterSeconds renders a Retry-After header value: whole seconds, rounded up,
// never below 1.
func retryAfterSeconds(d time.Duration) int {
	return circuitbreaker.RetryAfterSeconds(d)
}

var (
	inheritLimiter = newInheritConcurrencyLimiter(inheritMaxConcurrentPerServer)
	inheritBreaker = newInheritCircuitBreaker(inheritBreakerFailureThreshold, inheritBreakerWindow, inheritBreakerCooldown)
)

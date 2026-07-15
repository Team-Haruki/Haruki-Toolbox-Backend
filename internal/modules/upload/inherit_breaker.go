package upload

import (
	"context"
	"errors"
	"sync"
	"time"

	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/sekai"
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

type breakerPhase int

const (
	breakerClosed breakerPhase = iota
	breakerOpen
	breakerHalfOpen
)

type breakerState struct {
	phase    breakerPhase
	failures []time.Time
	openedAt time.Time
	// gen is bumped on every phase transition. Allow tags each admission with the gen
	// at admit time; RecordResult/ReleaseProbe ignore results whose token no longer
	// matches. This is what stops a slow request admitted in one epoch from mutating a
	// later epoch's state (e.g. a straggler success re-closing an open breaker, or a
	// straggler failure disrupting a fresh half-open probe).
	gen uint64
	// probePending marks that a half-open probe has been admitted and awaits its
	// result, so concurrent callers are rejected while it is in flight.
	probePending bool
}

// inheritCircuitBreaker is a per-server circuit breaker. It is closed normally,
// trips open after `threshold` degradation failures within `window`, rejects while
// open for `cooldown`, then admits a single probe (half-open) whose result closes or
// re-opens it. The clock is injectable for deterministic tests.
type inheritCircuitBreaker struct {
	mu        sync.Mutex
	threshold int
	window    time.Duration
	cooldown  time.Duration
	now       func() time.Time
	servers   map[harukiUtils.SupportedInheritUploadServer]*breakerState
}

func newInheritCircuitBreaker(threshold int, window, cooldown time.Duration) *inheritCircuitBreaker {
	if threshold <= 0 {
		threshold = 1
	}
	return &inheritCircuitBreaker{
		threshold: threshold,
		window:    window,
		cooldown:  cooldown,
		now:       time.Now,
		servers:   make(map[harukiUtils.SupportedInheritUploadServer]*breakerState),
	}
}

func (b *inheritCircuitBreaker) stateFor(server harukiUtils.SupportedInheritUploadServer) *breakerState {
	st, ok := b.servers[server]
	if !ok {
		st = &breakerState{phase: breakerClosed}
		b.servers[server] = st
	}
	return st
}

// Allow reports whether an inherit for server may proceed. When it returns false the
// second value is a suggested Retry-After. The third value is a token that a caller
// admitted (first value true) MUST pass to exactly one of RecordResult or
// ReleaseProbe so the epoch is resolved correctly; the token is 0 and meaningless
// when the request is rejected.
func (b *inheritCircuitBreaker) Allow(server harukiUtils.SupportedInheritUploadServer) (bool, time.Duration, uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.stateFor(server)
	now := b.now()
	switch st.phase {
	case breakerClosed:
		return true, 0, st.gen
	case breakerOpen:
		elapsed := now.Sub(st.openedAt)
		if elapsed < b.cooldown {
			return false, retryAfterFor(b.cooldown - elapsed), 0
		}
		// Cooldown elapsed: transition to half-open and admit a single probe.
		st.phase = breakerHalfOpen
		st.gen++
		st.probePending = true
		return true, 0, st.gen
	case breakerHalfOpen:
		if st.probePending {
			return false, retryAfterFor(b.cooldown), 0
		}
		// A prior probe was released without a verdict; admit a new probe in the same
		// epoch (no transition, so the gen is unchanged).
		st.probePending = true
		return true, 0, st.gen
	default:
		return true, 0, st.gen
	}
}

// ReleaseProbe undoes a half-open probe permit granted by Allow when the caller could
// not actually exercise the upstream (e.g. it was rejected by a downstream local
// gate). It records no health verdict, so a legitimately open/half-open breaker is
// not prematurely closed; the next admitted request probes again. A stale token
// (from a prior epoch) is ignored.
func (b *inheritCircuitBreaker) ReleaseProbe(server harukiUtils.SupportedInheritUploadServer, token uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.stateFor(server)
	if token != st.gen {
		return
	}
	if st.phase == breakerHalfOpen {
		st.probePending = false
	}
}

// RecordResult feeds an inherit outcome back to the breaker. token is the value Allow
// returned for this admission; degraded must be the result of
// inheritFailureIsUpstreamDegradation on the run error (false on success or on a user
// error, both of which prove the upstream is healthy). A result whose token no longer
// matches the current epoch is a straggler and is ignored.
func (b *inheritCircuitBreaker) RecordResult(server harukiUtils.SupportedInheritUploadServer, token uint64, degraded bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.stateFor(server)
	now := b.now()
	if token != st.gen {
		// Straggler admitted before a later phase transition: its outcome says nothing
		// about the current epoch, so ignore it. This prevents a slow success admitted
		// before the trip from re-closing an open breaker, and a slow failure from
		// disrupting a fresh half-open probe.
		return
	}

	switch st.phase {
	case breakerHalfOpen:
		st.probePending = false
		st.gen++
		if degraded {
			// Probe failed: re-open for another cooldown.
			st.phase = breakerOpen
			st.openedAt = now
		} else {
			st.phase = breakerClosed
		}
		st.failures = nil
	case breakerClosed:
		if !degraded {
			st.failures = nil
			return
		}
		st.failures = append(pruneBefore(st.failures, now.Add(-b.window)), now)
		if len(st.failures) >= b.threshold {
			st.phase = breakerOpen
			st.openedAt = now
			st.gen++
			st.failures = nil
		}
	case breakerOpen:
		// A current-epoch result while open should not occur (admissions in this epoch
		// are Closed or HalfOpen); ignore defensively so the cooldown is not disturbed.
	}
}

// pruneBefore drops timestamps at or before cutoff, keeping the slice's order.
func pruneBefore(ts []time.Time, cutoff time.Time) []time.Time {
	kept := ts[:0]
	for _, t := range ts {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	return kept
}

func retryAfterFor(d time.Duration) time.Duration {
	if d < inheritBreakerRetryAfterFloor {
		return inheritBreakerRetryAfterFloor
	}
	return d
}

// retryAfterSeconds renders a Retry-After header value: whole seconds, rounded up,
// never below 1.
func retryAfterSeconds(d time.Duration) int {
	secs := int((d + time.Second - 1) / time.Second)
	if secs < 1 {
		return 1
	}
	return secs
}

var (
	inheritLimiter = newInheritConcurrencyLimiter(inheritMaxConcurrentPerServer)
	inheritBreaker = newInheritCircuitBreaker(inheritBreakerFailureThreshold, inheritBreakerWindow, inheritBreakerCooldown)
)

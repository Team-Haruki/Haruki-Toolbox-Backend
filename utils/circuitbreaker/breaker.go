// Package circuitbreaker is a small keyed circuit breaker for upstream calls
// (game-API inherits, Sekai API profile lookups). Each key — normally a game
// server — has its own state, so one degraded server does not fail fast the
// others.
package circuitbreaker

import (
	"sync"
	"time"
)

type phase int

const (
	phaseClosed phase = iota
	phaseOpen
	phaseHalfOpen
)

type state struct {
	phase    phase
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

// Breaker is a per-key circuit breaker. It is closed normally, trips open after
// `threshold` degradation failures within `window`, rejects while open for
// `cooldown`, then admits a single probe (half-open) whose result closes or
// re-opens it. The clock is injectable for deterministic tests.
type Breaker[K comparable] struct {
	mu              sync.Mutex
	threshold       int
	window          time.Duration
	cooldown        time.Duration
	retryAfterFloor time.Duration
	now             func() time.Time
	states          map[K]*state
}

// New returns a breaker. retryAfterFloor is the minimum Retry-After that Allow
// reports while rejecting.
func New[K comparable](threshold int, window, cooldown, retryAfterFloor time.Duration) *Breaker[K] {
	if threshold <= 0 {
		threshold = 1
	}
	return &Breaker[K]{
		threshold:       threshold,
		window:          window,
		cooldown:        cooldown,
		retryAfterFloor: retryAfterFloor,
		now:             time.Now,
		states:          make(map[K]*state),
	}
}

// SetClock replaces the time source. Tests use it to advance time
// deterministically.
func (b *Breaker[K]) SetClock(now func() time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.now = now
}

func (b *Breaker[K]) stateFor(key K) *state {
	st, ok := b.states[key]
	if !ok {
		st = &state{phase: phaseClosed}
		b.states[key] = st
	}
	return st
}

// Allow reports whether a call for key may proceed. When it returns false the
// second value is a suggested Retry-After. The third value is a token that a caller
// admitted (first value true) MUST pass to exactly one of RecordResult or
// ReleaseProbe so the epoch is resolved correctly; the token is 0 and meaningless
// when the request is rejected.
func (b *Breaker[K]) Allow(key K) (bool, time.Duration, uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.stateFor(key)
	now := b.now()
	switch st.phase {
	case phaseOpen:
		elapsed := now.Sub(st.openedAt)
		if elapsed < b.cooldown {
			return false, b.retryAfter(b.cooldown - elapsed), 0
		}
		// Cooldown elapsed: transition to half-open and admit a single probe.
		st.phase = phaseHalfOpen
		st.gen++
		st.probePending = true
		return true, 0, st.gen
	case phaseHalfOpen:
		if st.probePending {
			return false, b.retryAfter(b.cooldown), 0
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
// gate, or its own context was cancelled). It records no health verdict, so a
// legitimately open/half-open breaker is not prematurely closed; the next admitted
// request probes again. A stale token (from a prior epoch) is ignored.
func (b *Breaker[K]) ReleaseProbe(key K, token uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.stateFor(key)
	if token != st.gen {
		return
	}
	if st.phase == phaseHalfOpen {
		st.probePending = false
	}
}

// RecordResult feeds an outcome back to the breaker. token is the value Allow
// returned for this admission; degraded reports whether the upstream misbehaved
// (false on success or on a user error, both of which prove the upstream is
// healthy). A result whose token no longer matches the current epoch is a straggler
// and is ignored.
func (b *Breaker[K]) RecordResult(key K, token uint64, degraded bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.stateFor(key)
	now := b.now()
	if token != st.gen {
		// Straggler admitted before a later phase transition: its outcome says nothing
		// about the current epoch, so ignore it. This prevents a slow success admitted
		// before the trip from re-closing an open breaker, and a slow failure from
		// disrupting a fresh half-open probe.
		return
	}

	switch st.phase {
	case phaseHalfOpen:
		st.probePending = false
		st.gen++
		if degraded {
			// Probe failed: re-open for another cooldown.
			st.phase = phaseOpen
			st.openedAt = now
		} else {
			st.phase = phaseClosed
		}
		st.failures = nil
	case phaseClosed:
		if !degraded {
			st.failures = nil
			return
		}
		st.failures = append(pruneBefore(st.failures, now.Add(-b.window)), now)
		if len(st.failures) >= b.threshold {
			st.phase = phaseOpen
			st.openedAt = now
			st.gen++
			st.failures = nil
		}
	default:
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

func (b *Breaker[K]) retryAfter(d time.Duration) time.Duration {
	if d < b.retryAfterFloor {
		return b.retryAfterFloor
	}
	return d
}

// RetryAfterSeconds renders a Retry-After header value: whole seconds, rounded up,
// never below 1.
func RetryAfterSeconds(d time.Duration) int {
	secs := int((d + time.Second - 1) / time.Second)
	if secs < 1 {
		return 1
	}
	return secs
}

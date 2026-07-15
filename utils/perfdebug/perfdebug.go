// Package perfdebug exposes a single process-wide toggle for opt-in performance
// instrumentation (pool/GC sampling, slow-request autopsies). It is off by default
// and flipped once at bootstrap from backend.profiling_enabled, so hot paths can gate
// diagnostic work behind a cheap atomic load without importing the config package.
package perfdebug

import "sync/atomic"

var enabled atomic.Bool

// SetEnabled sets the global profiling toggle. Called once during bootstrap.
func SetEnabled(v bool) {
	enabled.Store(v)
}

// Enabled reports whether opt-in profiling instrumentation should run. It is a single
// atomic load, safe to call on hot paths.
func Enabled() bool {
	return enabled.Load()
}

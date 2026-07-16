// Package mailnotify dispatches best-effort notification mail off the request
// path. Every caller already discarded the send result (log-only), so the HTTP
// response never depended on SMTP completing — blocking the handler on a
// 10-30s worst-case SMTP conversation bought nothing. Dispatch keeps the same
// delivery semantics while removing that latency from the user-visible request.
package mailnotify

import (
	"context"
	"time"
)

const (
	// maxConcurrentSends bounds background notification goroutines so an SMTP
	// outage under sustained traffic cannot grow goroutines without bound.
	maxConcurrentSends = 16
	// jobTimeout bounds the whole notification job (recipient DB query plus the
	// SMTP conversation, which additionally enforces its own dial/conversation
	// deadlines internally).
	jobTimeout = 30 * time.Second
)

var sendSlots = make(chan struct{}, maxConcurrentSends)

// Dispatch runs job on a background goroutine when a worker slot is free and
// falls back to running it synchronously when all slots are busy — saturation
// degrades to the pre-async behavior instead of dropping notifications or
// spawning unbounded goroutines. The job receives a background-derived context:
// callers must not capture the fiber request context (or any request-buffer
// backed strings) because fiber recycles them after the handler returns.
func Dispatch(job func(ctx context.Context)) {
	select {
	case sendSlots <- struct{}{}:
		go func() {
			defer func() { <-sendSlots }()
			run(job)
		}()
	default:
		run(job)
	}
}

func run(job func(ctx context.Context)) {
	ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
	defer cancel()
	job(ctx)
}

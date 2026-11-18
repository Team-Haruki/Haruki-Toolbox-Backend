// Package mailnotify admits best-effort mail jobs to a bounded background queue.
package mailnotify

import (
	"context"
	"sync"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
)

const (
	maxConcurrentSends = 16
	maxAdmittedJobs    = maxConcurrentSends + 128
	jobTimeout         = 30 * time.Second
)

var (
	admitted     = make(chan struct{}, maxAdmittedJobs)
	jobs         = make(chan func(context.Context), maxAdmittedJobs)
	startWorkers sync.Once
)

// Reservation holds space for one job. Its owner must call Send after a
// successful write or Cancel when the write fails or has no mail to send.
// Reservations are owned by a single request and are not shared concurrently.
type Reservation struct{ active bool }

// Reserve returns immediately. A caller that requires queue admission can
// reserve before writing, so saturation never turns a committed write into a
// retryable HTTP error.
func Reserve() *Reservation {
	select {
	case admitted <- struct{}{}:
		return &Reservation{active: true}
	default:
		return nil
	}
}

func (r *Reservation) Cancel() {
	if r != nil && r.active {
		r.active = false
		<-admitted
	}
}

// Send publishes a job using its reserved capacity. The job must only capture
// data owned by the caller, since Fiber reuses request buffers after return.
func (r *Reservation) Send(job func(context.Context)) {
	if !r.active {
		return
	}
	r.active = false
	startWorkers.Do(func() {
		for range maxConcurrentSends {
			go func() {
				for next := range jobs {
					ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
					next(ctx)
					cancel()
					<-admitted
				}
			}()
		}
	})
	// Every queued or running job owns one admission. A new job with a
	// reservation always fits even if every worker is busy.
	jobs <- job
}

// Dispatch is for best-effort read notifications that can proceed even when
// the queue is full. Saturation is logged without exposing email contents.
func Dispatch(job func(context.Context)) {
	reservation := Reserve()
	if reservation == nil {
		logger.Warnf("Mail notification queue full; skipping best-effort notification")
		return
	}
	reservation.Send(job)
}

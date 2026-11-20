// Package mailnotify admits best-effort mail jobs to a bounded background queue.
package mailnotify

import (
	"context"
	"sync"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
)

const (
	maxConcurrentSends  = 16
	securityMailWorkers = 4
	ticketMailWorkers   = maxConcurrentSends - securityMailWorkers
	securityMailBacklog = 32
	ticketMailBacklog   = 96
	jobTimeout          = 30 * time.Second
)

var (
	securityMail = newQueue(securityMailWorkers, securityMailBacklog)
	ticketMail   = newQueue(ticketMailWorkers, ticketMailBacklog)
)

type notificationQueue struct {
	admitted     chan struct{}
	jobs         chan func(context.Context)
	workers      int
	startWorkers sync.Once
}

func newQueue(workers, backlog int) *notificationQueue {
	availability := workers + backlog
	return &notificationQueue{admitted: make(chan struct{}, availability), jobs: make(chan func(context.Context), availability), workers: workers}
}

// Reservation holds space for one job. Its owner must call Send after a
// successful write or Cancel when the write fails or has no mail to send.
// Reservations are owned by a single request and are not shared concurrently.
type Reservation struct{ queue *notificationQueue }

// Reserve returns immediately. A caller that requires queue admission can
// reserve before writing, so saturation never turns a committed write into a
// retryable HTTP error.
func Reserve() *Reservation {
	return securityMail.reserve()
}

// ReserveTicket admits support mail independently of account notifications.
func ReserveTicket() *Reservation {
	return ticketMail.reserve()
}

func (q *notificationQueue) reserve() *Reservation {
	select {
	case q.admitted <- struct{}{}:
		return &Reservation{queue: q}
	default:
		return nil
	}
}

func (r *Reservation) Cancel() {
	if r != nil && r.queue != nil {
		<-r.queue.admitted
		r.queue = nil
	}
}

// Send publishes a job using its reserved capacity. The job must only capture
// data owned by the caller, since Fiber reuses request buffers after return.
func (r *Reservation) Send(job func(context.Context)) {
	q := r.queue
	if q == nil {
		return
	}
	r.queue = nil
	q.startWorkers.Do(func() {
		for range q.workers {
			go func() {
				for next := range q.jobs {
					ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
					next(ctx)
					cancel()
					<-q.admitted
				}
			}()
		}
	})
	// Every queued or running job owns one admission. A new job with a
	// reservation always fits even if every worker is busy.
	q.jobs <- job
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

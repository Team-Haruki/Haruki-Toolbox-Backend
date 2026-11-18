package mailnotify

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestDispatchBoundsQueueAndDrainsAcceptedJobs(t *testing.T) {
	maxAdmittedJobs := cap(securityMail.admitted)
	started := make(chan struct{}, maxAdmittedJobs)
	finished := make(chan struct{}, maxAdmittedJobs)
	unblock := make(chan struct{})
	var running, peak atomic.Int32
	for range maxAdmittedJobs {
		reservation := Reserve()
		if reservation == nil {
			t.Fatal("available queue slot was rejected")
		}
		reservation.Send(func(ctx context.Context) {
			if _, ok := ctx.Deadline(); !ok {
				t.Error("worker did not provide a deadline")
			}
			count := running.Add(1)
			for previous := peak.Load(); count > previous && !peak.CompareAndSwap(previous, count); previous = peak.Load() {
			}
			started <- struct{}{}
			<-unblock
			running.Add(-1)
			finished <- struct{}{}
		})
		reservation.Cancel()
	}
	deadline := time.After(3 * time.Second)
	for range securityMailWorkers {
		select {
		case <-started:
		case <-deadline:
			t.Fatal("workers did not start")
		}
	}
	if extra := Reserve(); extra != nil {
		extra.Cancel()
		t.Error("saturated queue admitted an extra job")
	}
	Dispatch(func(context.Context) { t.Error("saturated queue ran a best-effort job") })
	close(unblock)
	for range maxAdmittedJobs {
		select {
		case <-finished:
		case <-deadline:
			t.Fatal("accepted jobs did not drain")
		}
	}
	if peak.Load() != securityMailWorkers {
		t.Fatalf("peak concurrency = %d, want %d", peak.Load(), securityMailWorkers)
	}
	reservation := Reserve()
	if reservation == nil {
		t.Fatal("drained workers did not release admissions")
	}
	reservation.Cancel()
	reservation.Cancel()
}

func TestSaturatedTicketMailKeepsSecurityWorkersAvailable(t *testing.T) {
	started := make(chan struct{}, ticketMailWorkers)
	unblock := make(chan struct{})
	defer close(unblock)
	for range ticketMailWorkers {
		reservation := ReserveTicket()
		if reservation == nil {
			t.Fatal("ticket worker was not available")
		}
		reservation.Send(func(context.Context) {
			started <- struct{}{}
			<-unblock
		})
	}
	for range ticketMailBacklog {
		reservation := ReserveTicket()
		if reservation == nil {
			t.Fatal("ticket backlog filled too early")
		}
		defer reservation.Cancel()
	}
	deadline := time.After(3 * time.Second)
	for range ticketMailWorkers {
		select {
		case <-started:
		case <-deadline:
			t.Fatal("ticket workers did not start")
		}
	}
	if extra := ReserveTicket(); extra != nil {
		extra.Cancel()
		t.Fatal("extra ticket job admitted while support queue was full")
	}
	securitySent := make(chan struct{})
	Dispatch(func(context.Context) { close(securitySent) })
	select {
	case <-securitySent:
	case <-deadline:
		t.Fatal("ticket mail blocked the account security queue")
	}
}

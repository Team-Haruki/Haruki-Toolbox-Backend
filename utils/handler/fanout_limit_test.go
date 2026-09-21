package handler

import (
	"sync"
	"testing"
	"time"
)

func waitFanoutState(t *testing.T, l *fanoutLimiter, waiting int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if l.snapshot().Waiting == waiting {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("waiting=%d want=%d", l.snapshot().Waiting, waiting)
}

func TestFanoutAdmissionFIFOAndOversizedInput(t *testing.T) {
	l := newFanoutLimiter(2, 10)
	first := l.acquire(6)
	acquired := make(chan func(), 1)
	go func() { acquired <- l.acquire(20) }()
	waitFanoutState(t, l, 1)
	second := make(chan func(), 1)
	go func() { second <- l.acquire(1) }()
	waitFanoutState(t, l, 2)
	first()
	var oversized func()
	select {
	case oversized = <-acquired:
	case <-time.After(time.Second):
		t.Fatal("oversized input starved")
	}
	st := l.snapshot()
	if st.Active != 1 || st.ActiveInputBytes != 20 || st.Waiting != 1 {
		t.Fatalf("oversized not exclusive: %+v", st)
	}
	select {
	case <-second:
		t.Fatal("small input bypassed exclusive oversized input")
	default:
	}
	oversized()
	oversized() // release is idempotent on cleanup/error paths
	select {
	case release := <-second:
		release()
	case <-time.After(time.Second):
		t.Fatal("waiter not resumed")
	}
	if st := l.snapshot(); st.Active != 0 || st.ActiveInputBytes != 0 || st.Waiting != 0 || st.Completed != 3 {
		t.Fatalf("leaked permits: %+v", st)
	}
}

func TestFanoutBoundedSlowDeliveryKeepsEveryTask(t *testing.T) {
	for _, size := range []int64{1 << 20, 10 << 20} {
		l := newFanoutLimiter(4, 64<<20)
		var wg sync.WaitGroup
		for range 24 {
			wg.Go(func() {
				release := l.acquire(size)
				defer release()
				// Represents copying only AFTER admission, then a slow receiver.
				payload := make([]byte, int(size))
				payload[len(payload)-1] = 1
				time.Sleep(5 * time.Millisecond)
				if payload[len(payload)-1] != 1 {
					t.Error("payload changed")
				}
			})
		}
		wg.Wait()
		st := l.snapshot()
		if st.Completed != 24 || st.Active != 0 || st.Waiting != 0 || st.PeakTasks > 4 || st.PeakInputBytes > 64<<20 {
			t.Fatalf("delivery lost or capacity exceeded: %+v", st)
		}
		t.Logf("input=%d completed=%d peak_tasks=%d peak_input_bytes=%d", size, st.Completed, st.PeakTasks, st.PeakInputBytes)
	}
}

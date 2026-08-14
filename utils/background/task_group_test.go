package background

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTaskGroupShutdownSealsAdmissionAndDrainsAcceptedTasks(t *testing.T) {
	t.Parallel()

	group := NewTaskGroup(nil)
	started := make(chan struct{})
	release := make(chan struct{})
	if !group.Go("blocked", func() {
		close(started)
		<-release
	}) {
		t.Fatal("initial task was rejected")
	}
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := group.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want deadline exceeded", err)
	}
	if group.Go("late", func() {}) {
		t.Fatal("task was accepted after shutdown sealed the group")
	}

	close(release)
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := group.Shutdown(ctx); err != nil {
		t.Fatalf("second Shutdown returned error: %v", err)
	}
}

func TestTaskGroupConcurrentAdmissionAndShutdownHasNoMissedTasks(t *testing.T) {
	t.Parallel()

	group := NewTaskGroup(nil)
	const submissions = 256
	var accepted atomic.Int64
	var completed atomic.Int64
	start := make(chan struct{})
	var submitters sync.WaitGroup
	for range submissions {
		submitters.Add(1)
		go func() {
			defer submitters.Done()
			<-start
			if group.Go("race", func() { completed.Add(1) }) {
				accepted.Add(1)
			}
		}()
	}
	close(start)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := group.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
	submitters.Wait()
	if got, want := completed.Load(), accepted.Load(); got != want {
		t.Fatalf("completed tasks = %d, accepted tasks = %d", got, want)
	}
	if group.Go("late", func() {}) {
		t.Fatal("late task was accepted")
	}
}

func TestTaskGroupRecoversTaskPanicAndStillDrains(t *testing.T) {
	t.Parallel()

	panicObserved := make(chan any, 1)
	group := NewTaskGroup(func(_ string, recovered any) {
		panicObserved <- recovered
	})
	if !group.Go("panic", func() { panic("boom") }) {
		t.Fatal("panic task was rejected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := group.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
	select {
	case got := <-panicObserved:
		if got != "boom" {
			t.Fatalf("recovered panic = %v, want boom", got)
		}
	default:
		t.Fatal("panic handler was not called")
	}
}

func TestInlineRunnerRunsBeforeReturning(t *testing.T) {
	t.Parallel()

	run := false
	if !(InlineRunner{}).Go("inline", func() { run = true }) {
		t.Fatal("inline task was rejected")
	}
	if !run {
		t.Fatal("inline task did not finish before Go returned")
	}
}

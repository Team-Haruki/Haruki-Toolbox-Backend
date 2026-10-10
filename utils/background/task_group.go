package background

import (
	"context"
	"fmt"
	"sync"
)

// Runner accepts asynchronous work. A false return means the task was not
// started, normally because the process is already draining background work.
type Runner interface {
	Go(name string, task func()) bool
}

// ShutdownNotifier is implemented by runners that can tell long-lived tasks,
// such as a retry loop waiting out a backoff, that shutdown has started. The
// returned channel is closed once the runner stops accepting work; a nil
// channel means shutdown is never signalled.
type ShutdownNotifier interface {
	ShuttingDown() <-chan struct{}
}

// ShutdownSignal returns runner's shutdown channel, or nil when runner cannot
// signal shutdown. Receiving from a nil channel blocks forever, so callers can
// select on the result unconditionally.
func ShutdownSignal(runner Runner) <-chan struct{} {
	if notifier, ok := runner.(ShutdownNotifier); ok {
		return notifier.ShuttingDown()
	}
	return nil
}

// PanicHandler observes a panic recovered at a task boundary.
type PanicHandler func(name string, recovered any)

// TaskGroup tracks process-owned background work without the Add/Wait race of
// a WaitGroup shared between request handlers and shutdown. Admission, sealing,
// and the active count all change under one mutex; once Shutdown seals the
// group, no new task can increment that count.
type TaskGroup struct {
	mu           sync.Mutex
	accepting    bool
	active       int
	drained      chan struct{}
	shuttingDown chan struct{}
	panicHandler PanicHandler
}

// NewTaskGroup returns an accepting, empty task group.
func NewTaskGroup(panicHandler PanicHandler) *TaskGroup {
	drained := make(chan struct{})
	close(drained)
	return &TaskGroup{
		accepting:    true,
		drained:      drained,
		shuttingDown: make(chan struct{}),
		panicHandler: panicHandler,
	}
}

// Go starts task when the group is still accepting work. The task is counted
// before its goroutine is created, so a concurrent Shutdown cannot miss it.
func (g *TaskGroup) Go(name string, task func()) bool {
	if g == nil || task == nil {
		return false
	}

	g.mu.Lock()
	if !g.accepting {
		g.mu.Unlock()
		return false
	}
	if g.active == 0 {
		g.drained = make(chan struct{})
	}
	g.active++
	g.mu.Unlock()

	go g.run(name, task)
	return true
}

func (g *TaskGroup) run(name string, task func()) {
	defer g.taskDone()
	if g.panicHandler != nil {
		defer func() {
			if recovered := recover(); recovered != nil {
				g.panicHandler(name, recovered)
			}
		}()
	}
	// With no handler installed, do not recover: a panic keeps normal Go
	// semantics instead of being silently swallowed. Production injects a
	// process logger as its handler.
	task()
}

func (g *TaskGroup) taskDone() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active--
	if g.active == 0 {
		close(g.drained)
	}
}

// Shutdown atomically stops admission and waits for every task admitted before
// that point. It is safe to call repeatedly; after a timeout, a later call may
// continue waiting on the same sealed group.
func (g *TaskGroup) Shutdown(ctx context.Context) error {
	if g == nil {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("background task shutdown context is nil")
	}

	g.mu.Lock()
	if g.accepting && g.shuttingDown != nil {
		close(g.shuttingDown)
	}
	g.accepting = false
	drained := g.drained
	g.mu.Unlock()

	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ShuttingDown returns a channel that is closed when Shutdown seals the group,
// so admitted tasks that wait (for example between retries) can stop early
// instead of holding up the drain.
func (g *TaskGroup) ShuttingDown() <-chan struct{} {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.shuttingDown
}

// InlineRunner runs accepted work in the caller. It is used by an already
// tracked parent task when all of its follow-up work must remain inside that
// parent's lifetime instead of attempting admission after shutdown has sealed
// the application task group.
type InlineRunner struct {
	// Shutdown, when set, is reported by ShuttingDown so inline work can
	// observe the parent's shutdown (see ShutdownSignal).
	Shutdown <-chan struct{}
}

// ShuttingDown returns the parent's shutdown channel, or nil when unset.
func (r InlineRunner) ShuttingDown() <-chan struct{} {
	return r.Shutdown
}

func (InlineRunner) Go(_ string, task func()) bool {
	if task == nil {
		return false
	}
	task()
	return true
}

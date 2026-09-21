package handler

import (
	"sync"
	"time"
)

// The budget bounds admitted raw input, not decoded maps or the process RSS.
// One oversized input is admitted alone so committed uploads are never dropped.
// Admission happens in the caller BEFORE copying raw bytes or starting a task.
const fanoutMaxTasks = 4
const fanoutInputBudget int64 = 64 << 20

var uploadFanoutLimit = newFanoutLimiter(fanoutMaxTasks, fanoutInputBudget)

type fanoutWaiter struct{ started time.Time }
type fanoutLimiter struct {
	mu        sync.Mutex
	changed   *sync.Cond
	maxTasks  int
	maxBytes  int64
	active    int
	bytes     int64
	queue     []*fanoutWaiter
	peakTasks int
	peakBytes int64
	completed uint64
}
type FanoutSnapshot struct {
	Active           int    `json:"active"`
	ActiveInputBytes int64  `json:"active_input_bytes"`
	Waiting          int    `json:"waiting"`
	OldestWaitMS     int64  `json:"oldest_wait_ms"`
	PeakTasks        int    `json:"peak_tasks"`
	PeakInputBytes   int64  `json:"peak_input_bytes"`
	Completed        uint64 `json:"completed"`
	MaxTasks         int    `json:"max_tasks"`
	InputBudgetBytes int64  `json:"input_budget_bytes"`
}

func newFanoutLimiter(tasks int, bytes int64) *fanoutLimiter {
	l := &fanoutLimiter{maxTasks: tasks, maxBytes: bytes}
	l.changed = sync.NewCond(&l.mu)
	return l
}
func (l *fanoutLimiter) acquire(bytes int64) func() {
	bytes = max(0, bytes)
	l.mu.Lock()
	fits := func() bool { return l.active < l.maxTasks && (l.active == 0 || bytes <= l.maxBytes-l.bytes) }
	if len(l.queue) != 0 || !fits() {
		waiter := &fanoutWaiter{started: time.Now()}
		l.queue = append(l.queue, waiter)
		for l.queue[0] != waiter || !fits() {
			l.changed.Wait()
		}
		l.queue[0] = nil
		l.queue = l.queue[1:]
	}
	l.active++
	l.bytes += bytes
	l.peakTasks = max(l.peakTasks, l.active)
	l.peakBytes = max(l.peakBytes, l.bytes)
	l.changed.Broadcast()
	l.mu.Unlock()
	return sync.OnceFunc(func() {
		l.mu.Lock()
		l.active--
		l.bytes -= bytes
		l.completed++
		l.changed.Broadcast()
		l.mu.Unlock()
	})
}
func (l *fanoutLimiter) snapshot() FanoutSnapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := FanoutSnapshot{Active: l.active, ActiveInputBytes: l.bytes, Waiting: len(l.queue), PeakTasks: l.peakTasks, PeakInputBytes: l.peakBytes, Completed: l.completed, MaxTasks: l.maxTasks, InputBudgetBytes: l.maxBytes}
	if len(l.queue) > 0 {
		st.OldestWaitMS = time.Since(l.queue[0].started).Milliseconds()
	}
	return st
}
func UploadFanoutStats() FanoutSnapshot { return uploadFanoutLimit.snapshot() }

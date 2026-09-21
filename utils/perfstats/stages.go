// Package perfstats records bounded, process-wide stage histograms. It never
// accepts request labels, user identifiers, URLs, payloads, or credentials.
package perfstats

import (
	"sync"
	"time"
)

type Stage uint8

const (
	UploadDecode Stage = iota
	UploadPreprocess
	UploadPersist
	FanoutAdmission
	SyncEncodeWait
	SyncCheck
	SyncProcessed
	SyncRestored
	SyncDelivery
	stageCount
)

var names = [stageCount]string{"upload_decode", "upload_preprocess", "upload_persist", "fanout_admission", "sync_encode_wait", "sync_check", "sync_processed", "sync_restored", "sync_delivery"}

// Non-cumulative buckets: <=1ms, <=5ms, <=20ms, <=100ms, <=500ms, <=2s, >2s.
var bounds = [...]time.Duration{time.Millisecond, 5 * time.Millisecond, 20 * time.Millisecond, 100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second}

type Histogram struct {
	Count   uint64    `json:"count"`
	TotalNS int64     `json:"total_ns"`
	MaxNS   int64     `json:"max_ns"`
	Buckets [7]uint64 `json:"buckets"`
}

var registry struct {
	sync.Mutex
	stages [stageCount]Histogram
}

func Track(stage Stage) func() {
	started := time.Now()
	return func() { Observe(stage, time.Since(started)) }
}
func Observe(stage Stage, elapsed time.Duration) {
	if stage >= stageCount || elapsed < 0 {
		return
	}
	bucket := len(bounds)
	for i, bound := range bounds {
		if elapsed <= bound {
			bucket = i
			break
		}
	}
	registry.Lock()
	h := &registry.stages[stage]
	h.Count++
	h.TotalNS += int64(elapsed)
	h.MaxNS = max(h.MaxNS, int64(elapsed))
	h.Buckets[bucket]++
	registry.Unlock()
}
func Snapshot() map[string]Histogram {
	registry.Lock()
	values := registry.stages
	registry.Unlock()
	out := make(map[string]Histogram, len(names))
	for i, name := range names {
		out[name] = values[i]
	}
	return out
}

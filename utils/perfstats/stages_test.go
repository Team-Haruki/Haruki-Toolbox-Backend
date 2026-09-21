package perfstats

import (
	"sync"
	"testing"
	"time"
)

func TestHistogramConcurrentCountsAndBounds(t *testing.T) {
	before := Snapshot()[names[UploadDecode]]
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() { Observe(UploadDecode, time.Millisecond); Observe(UploadDecode, 3*time.Second) })
	}
	wg.Wait()
	after := Snapshot()[names[UploadDecode]]
	if after.Count-before.Count != 200 || after.Buckets[0]-before.Buckets[0] != 100 || after.Buckets[6]-before.Buckets[6] != 100 {
		t.Fatalf("lost observations: before=%+v after=%+v", before, after)
	}
	if after.MaxNS < int64(3*time.Second) {
		t.Fatal("maximum missing")
	}
	Observe(stageCount, time.Second)
	if len(Snapshot()) != int(stageCount) {
		t.Fatal("unbounded stage labels")
	}
}

package upload

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestBirthdayPendingCountQuotaIsAtomicAndAckReleases(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	manager := &harukiRedis.HarukiRedisManager{Redis: client}
	monitor := &BirthdayMonitorMirror{SubscriptionID: "sub", SubscriptionVersion: "v1", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	const attempts = birthdayPendingPerSubscription + 6
	type result struct {
		event *BirthdayMonitorEvent
		err   error
	}
	out := make(chan result, attempts)
	var wg sync.WaitGroup
	for i := range attempts {
		wg.Go(func() {
			event, err := StoreBirthdayMonitorEvent(t.Context(), manager, monitor, BirthdayMonitorEvent{EventID: fmt.Sprintf("event-%d", i)})
			out <- result{event, err}
		})
	}
	wg.Wait()
	close(out)
	var admitted []*BirthdayMonitorEvent
	for item := range out {
		if item.err == nil {
			admitted = append(admitted, item.event)
		} else if !errors.Is(item.err, errBirthdayPendingQuota) {
			t.Fatal(item.err)
		}
	}
	if len(admitted) != birthdayPendingPerSubscription {
		t.Fatalf("stored %d events, want %d", len(admitted), birthdayPendingPerSubscription)
	}
	duplicate := BirthdayMonitorEvent{EventID: admitted[0].EventID, UploadTime: 9}
	event, err := StoreBirthdayMonitorEvent(t.Context(), manager, monitor, duplicate)
	if err != nil || event.UploadTime != admitted[0].UploadTime {
		t.Fatalf("retry overwrote an admitted event: %+v %v", event, err)
	}
	if err := AckBirthdayMonitorEvent(t.Context(), manager, admitted[0].EventID, monitor.SubscriptionID, monitor.SubscriptionVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := StoreBirthdayMonitorEvent(t.Context(), manager, monitor, BirthdayMonitorEvent{EventID: "replacement"}); err != nil {
		t.Fatalf("acknowledgement did not release quota: %v", err)
	}
}

func TestBirthdayPendingByteQuotaReclaimsExpiredEvents(t *testing.T) {
	server := miniredis.RunT(t)
	now := time.Now()
	server.SetTime(now)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := func(key, sub string, globalLimit, subLimit int) error {
		keys := append(append([]string(nil), birthdayPendingKeys...), key)
		return client.Eval(t.Context(), birthdayStoreLua, keys, "12345678", sub, 10, globalLimit, 10, subLimit, 1000).Err()
	}
	if err := store("event-one", "sub-1", 15, 10); err != nil {
		t.Fatal(err)
	}
	if err := store("event-two", "sub-1", 50, 10); !errors.Is(err, redis.Nil) {
		t.Fatalf("subscription byte limit: %v", err)
	}
	if err := store("event-three", "sub-2", 15, 10); !errors.Is(err, redis.Nil) {
		t.Fatalf("global byte limit: %v", err)
	}
	server.FastForward(time.Second)
	server.SetTime(now.Add(time.Second))
	if err := store("event-four", "sub-2", 15, 10); err != nil {
		t.Fatalf("capacity after expiry: %v", err)
	}
}

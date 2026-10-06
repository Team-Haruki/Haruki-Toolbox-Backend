package upload

import (
	"context"
	"sync"
	"testing"
	"time"

	manager "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestIngressCounters(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	defer client.Close()
	r := &manager.HarukiRedisManager{Redis: client}
	at := time.Now().UTC()
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { RecordProxyIngress(r, at, "3", "accepted") })
	}
	wg.Wait()
	key := IngressKey(at, "3", "accepted")
	n, err := client.Get(context.Background(), key).Int64()
	if err != nil || n != 20 {
		t.Fatalf("counter=%d %v", n, err)
	}
	if ttl := mini.TTL(key); ttl <= 0 || ttl > IngressRetention {
		t.Fatal(ttl)
	}
	RecordProxyIngress(r, at, "3", "arbitrary-client-label")
	if mini.Exists(IngressKey(at, "3", "arbitrary-client-label")) {
		t.Fatal("unbounded label")
	}
	if !mini.Exists(IngressKey(at, "3", "internal_error")) {
		t.Fatal("missing normalized result")
	}
	RecordProxyIngress(r, at, "invalid", "accepted")
	if mini.Exists(IngressKey(at, "invalid", "accepted")) {
		t.Fatal("invalid protocol label")
	}
}

func TestIngressUnavailableRecordsFailure(t *testing.T) {
	oldFailure, oldWarning := ingressLastFailure.Load(), ingressLastWarning.Load()
	t.Cleanup(func() { ingressLastFailure.Store(oldFailure); ingressLastWarning.Store(oldWarning) })
	ingressLastWarning.Store(0)
	start := time.Now().Add(-time.Second)
	RecordProxyIngress(nil, time.Now(), "3", "accepted")
	if IngressLastFailure().Before(start) {
		t.Fatal("lost failure signal")
	}
	RecordProxyIngress(nil, time.Now(), "3", "accepted")
	if ingressLastWarning.Load() == 0 {
		t.Fatal("missing warning throttle timestamp")
	}
}

package runtimeconfig

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func newRuntimeConfigRedisStore(t *testing.T) (Store, *miniredis.Miniredis) {
	t.Helper()
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error: %v", err)
	}
	t.Cleanup(server.Close)

	client := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("redis client close: %v", err)
		}
	})
	return NewRedisStore(&redis.HarukiRedisManager{Redis: client}), server
}

func TestRedisStoreAtomicallyMergesConcurrentServiceUpdates(t *testing.T) {
	store, server := newRuntimeConfigRedisStore(t)
	for i := 0; i < 100; i++ {
		server.Del(redis.BuildRuntimeConfigKey())
		first := New(Snapshot{}, store)
		second := New(Snapshot{}, store)
		token := "token"
		secret := "secret"

		start := make(chan struct{})
		errs := make(chan error, 2)
		var ready sync.WaitGroup
		ready.Add(2)
		go func() {
			ready.Done()
			<-start
			errs <- first.Update(t.Context(), Update{PrivateAPIToken: &token})
		}()
		go func() {
			ready.Done()
			<-start
			errs <- second.Update(t.Context(), Update{WebhookJWTSecret: &secret})
		}()
		ready.Wait()
		close(start)
		for range 2 {
			if err := <-errs; err != nil {
				t.Fatalf("iteration %d Update returned error: %v", i, err)
			}
		}

		raw, err := server.Get(redis.BuildRuntimeConfigKey())
		if err != nil {
			t.Fatalf("iteration %d read persisted snapshot: %v", i, err)
		}
		var got Snapshot
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("iteration %d decode persisted snapshot: %v", i, err)
		}
		if got.PrivateAPIToken != token || got.WebhookJWTSecret != secret {
			t.Fatalf("iteration %d lost a partial update: %#v", i, got)
		}
	}
}

func TestRedisStorePreservesSnapshotJSONContract(t *testing.T) {
	store, server := newRuntimeConfigRedisStore(t)
	token := "token"
	service := New(Snapshot{}, store)
	if err := service.Update(t.Context(), Update{PrivateAPIToken: &token}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	raw, err := server.Get(redis.BuildRuntimeConfigKey())
	if err != nil {
		t.Fatalf("read persisted snapshot: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		t.Fatalf("decode persisted snapshot: %v", err)
	}
	wantFields := []string{
		"publicApiAllowedKeys",
		"privateApiToken",
		"privateApiUserAgent",
		"harukiProxyUserAgent",
		"harukiProxyVersion",
		"harukiProxySecret",
		"harukiProxyUnpackKey",
		"webhookJwtSecret",
	}
	if len(fields) != len(wantFields) {
		t.Fatalf("persisted fields = %v, want exactly %v", fields, wantFields)
	}
	for _, field := range wantFields {
		if _, ok := fields[field]; !ok {
			t.Errorf("persisted snapshot missing field %q", field)
		}
	}
	if _, ok := fields["webhookEnabled"]; ok {
		t.Error("nil webhookEnabled must remain omitted")
	}

	enabled := false
	if err := service.Update(t.Context(), Update{WebhookEnabled: &enabled}); err != nil {
		t.Fatalf("Update webhookEnabled returned error: %v", err)
	}
	raw, err = server.Get(redis.BuildRuntimeConfigKey())
	if err != nil {
		t.Fatalf("read updated persisted snapshot: %v", err)
	}
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		t.Fatalf("decode updated persisted snapshot: %v", err)
	}
	if len(fields) != 9 {
		t.Fatalf("persisted field count with webhookEnabled = %d, want 9", len(fields))
	}
	if got := string(fields["webhookEnabled"]); got != "false" {
		t.Fatalf("webhookEnabled = %s, want false", got)
	}
}

func TestRedisStoreSeedsMissingRecordFromServiceSnapshot(t *testing.T) {
	store, _ := newRuntimeConfigRedisStore(t)
	service := New(Snapshot{
		PrivateAPIToken:      "startup-token",
		HarukiProxyUserAgent: "startup-agent",
	}, store)
	secret := "runtime-secret"
	if err := service.Update(t.Context(), Update{WebhookJWTSecret: &secret}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	got, found, err := store.Load(t.Context())
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !found {
		t.Fatal("runtime snapshot was not persisted")
	}
	if got.PrivateAPIToken != "startup-token" ||
		got.HarukiProxyUserAgent != "startup-agent" ||
		got.WebhookJWTSecret != secret {
		t.Fatalf("persisted snapshot did not merge startup fallback: %#v", got)
	}
}

func TestRedisStoreDecodeFailureDoesNotPublishLocalSnapshot(t *testing.T) {
	store, server := newRuntimeConfigRedisStore(t)
	server.Set(redis.BuildRuntimeConfigKey(), "{invalid")
	service := New(Snapshot{PrivateAPIToken: "local-token"}, store)
	token := "new-token"
	if err := service.Update(t.Context(), Update{PrivateAPIToken: &token}); err == nil {
		t.Fatal("Update should fail for an invalid persisted snapshot")
	}
	if got := service.localSnapshot().PrivateAPIToken; got != "local-token" {
		t.Fatalf("failed update published local token %q", got)
	}
}

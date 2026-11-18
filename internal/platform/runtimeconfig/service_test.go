package runtimeconfig

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type memoryStore struct {
	mu       sync.Mutex
	snapshot Snapshot
	found    bool
	loadErr  error
	saveErr  error
}

func (s *memoryStore) Load(context.Context) (Snapshot, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneSnapshot(s.snapshot), s.found, s.loadErr
}

func (s *memoryStore) Apply(_ context.Context, update Update, fallback Snapshot) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return Snapshot{}, s.saveErr
	}
	if !s.found {
		s.snapshot = cloneSnapshot(fallback)
	}
	applyUpdate(&s.snapshot, update)
	s.found = true
	return cloneSnapshot(s.snapshot), nil
}

func TestServiceCopiesSnapshotsAndUpdates(t *testing.T) {
	enabled := true
	service := New(Snapshot{
		AllowedKeys:    []string{"a"},
		WebhookEnabled: &enabled,
	}, nil)

	first, err := service.Current(t.Context())
	if err != nil {
		t.Fatalf("Current returned error: %v", err)
	}
	first.AllowedKeys[0] = "mutated"
	*first.WebhookEnabled = false

	second, err := service.Current(t.Context())
	if err != nil {
		t.Fatalf("Current returned error: %v", err)
	}
	if !reflect.DeepEqual(second.AllowedKeys, []string{"a"}) {
		t.Fatalf("Current leaked public key slice: %#v", second.AllowedKeys)
	}
	if second.WebhookEnabled == nil || !*second.WebhookEnabled {
		t.Fatalf("Current leaked webhook enabled pointer")
	}

	keys := []string{"b", "c"}
	secret := "rotated"
	if err := service.Update(t.Context(), Update{
		AllowedKeys:      &keys,
		WebhookJWTSecret: &secret,
	}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	keys[0] = "changed"
	updated, _ := service.Current(t.Context())
	if !reflect.DeepEqual(updated.AllowedKeys, []string{"b", "c"}) || updated.WebhookJWTSecret != "rotated" {
		t.Fatalf("updated snapshot = %#v", updated)
	}
}

func TestServiceSharesPersistedSnapshot(t *testing.T) {
	store := &memoryStore{}
	first := New(Snapshot{}, store)
	second := New(Snapshot{}, store)
	token := "shared-token"
	if err := first.Update(t.Context(), Update{PrivateAPIToken: &token}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	snapshot, err := second.Current(t.Context())
	if err != nil {
		t.Fatalf("Current returned error: %v", err)
	}
	if snapshot.PrivateAPIToken != token {
		t.Fatalf("PrivateAPIToken = %q, want %q", snapshot.PrivateAPIToken, token)
	}
}

func TestServicesAtomicallyMergeConcurrentUpdates(t *testing.T) {
	for i := 0; i < 100; i++ {
		store := &memoryStore{}
		first := New(Snapshot{}, store)
		second := New(Snapshot{}, store)
		token := "token"
		secret := "secret"
		start := make(chan struct{})
		errs := make(chan error, 2)
		go func() {
			<-start
			errs <- first.Update(t.Context(), Update{PrivateAPIToken: &token})
		}()
		go func() {
			<-start
			errs <- second.Update(t.Context(), Update{WebhookJWTSecret: &secret})
		}()
		close(start)
		for range 2 {
			if err := <-errs; err != nil {
				t.Fatalf("Update returned error: %v", err)
			}
		}

		store.mu.Lock()
		got := cloneSnapshot(store.snapshot)
		store.mu.Unlock()
		if got.PrivateAPIToken != token || got.WebhookJWTSecret != secret {
			t.Fatalf("iteration %d lost a partial update: %#v", i, got)
		}
	}
}

func TestServiceDoesNotPublishFailedSave(t *testing.T) {
	wantErr := errors.New("save failed")
	store := &memoryStore{saveErr: wantErr}
	service := New(Snapshot{PrivateAPIToken: "old"}, store)
	token := "new"
	if err := service.Update(t.Context(), Update{PrivateAPIToken: &token}); !errors.Is(err, wantErr) {
		t.Fatalf("Update error = %v, want %v", err, wantErr)
	}

	store.mu.Lock()
	store.saveErr = nil
	store.loadErr = errors.New("offline")
	store.mu.Unlock()
	snapshot, err := service.Current(t.Context())
	if err == nil {
		t.Fatalf("Current should report store failure")
	}
	if snapshot.PrivateAPIToken != "old" {
		t.Fatalf("failed update published snapshot: %#v", snapshot)
	}
}

type pausingRuntimeStore struct {
	memoryStore
	entered chan struct{}
	unblock chan struct{}
}

func (s *pausingRuntimeStore) Load(ctx context.Context) (Snapshot, bool, error) {
	snapshot, found, err := s.memoryStore.Load(ctx)
	s.entered <- struct{}{}
	select {
	case <-s.unblock:
		return snapshot, found, err
	case <-ctx.Done():
		return Snapshot{}, false, ctx.Err()
	}
}

func TestRuntimeReadAdmissionLeavesCredentialRotationIndependent(t *testing.T) {
	store := &pausingRuntimeStore{
		memoryStore: memoryStore{snapshot: Snapshot{PrivateAPIToken: "old"}, found: true},
		entered:     make(chan struct{}, maxConcurrentRefreshes),
		unblock:     make(chan struct{}),
	}
	service := New(Snapshot{}, store)
	service.timeout = 3 * time.Second
	defer close(store.unblock)
	type result struct {
		snapshot Snapshot
		err      error
	}
	out := make(chan result, maxConcurrentRefreshes)
	for range maxConcurrentRefreshes {
		go func() { snapshot, err := service.Current(t.Context()); out <- result{snapshot, err} }()
	}
	for range maxConcurrentRefreshes {
		select {
		case <-store.entered:
		case <-time.After(time.Second):
			t.Fatal("refresh did not reach store")
		}
	}
	if _, err := service.Current(t.Context()); !errors.Is(err, errRefreshBusy) {
		t.Fatalf("excess refresh: %v", err)
	}
	committed := make(chan error, 1)
	go func() {
		token := "new"
		committed <- service.Update(t.Context(), Update{PrivateAPIToken: &token})
	}()
	select {
	case err := <-committed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("credential rotation waited on public reads")
	}
	for range maxConcurrentRefreshes {
		store.unblock <- struct{}{}
	}
	for range maxConcurrentRefreshes {
		item := <-out
		if item.err != nil || item.snapshot.PrivateAPIToken != "new" {
			t.Fatalf("stale read survived rotation: %+v", item)
		}
	}
}

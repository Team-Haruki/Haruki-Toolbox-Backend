package runtimeconfig

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"
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
	s.snapshot.Version++
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
	releaseReads := sync.OnceFunc(func() { close(store.unblock) })
	defer releaseReads()
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
	if _, err := service.Current(t.Context()); !errors.Is(err, ErrRefreshBusy) {
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
	releaseReads()
	for range maxConcurrentRefreshes {
		item := <-out
		if item.err != nil || item.snapshot.PrivateAPIToken != "new" {
			t.Fatalf("stale read survived rotation: %+v", item)
		}
	}
}

func TestCredentialRefreshWaitsForItsOwnStoreRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &pausingRuntimeStore{
			memoryStore: memoryStore{snapshot: Snapshot{PrivateAPIToken: "retired", Version: 1}, found: true},
			entered:     make(chan struct{}, maxConcurrentRefreshes),
			unblock:     make(chan struct{}),
		}
		service := New(Snapshot{PrivateAPIToken: "retired", Version: 1}, store)
		for range maxConcurrentRefreshes {
			go func() { _, _ = service.Current(t.Context()) }()
		}
		for range maxConcurrentRefreshes {
			<-store.entered
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
		defer cancel()
		if snapshot, err := service.CurrentForAuthentication(ctx); !errors.Is(err, context.DeadlineExceeded) || snapshot.PrivateAPIToken != "" {
			t.Fatal("an expired credential read returned a token or skipped its deadline")
		}

		type result struct {
			snapshot Snapshot
			err      error
		}
		read := make(chan result, 1)
		go func() {
			snapshot, err := service.CurrentForAuthentication(t.Context())
			read <- result{snapshot, err}
		}()
		synctest.Wait()
		select {
		case <-read:
			t.Fatal("the credential read returned while the slots were full")
		default:
		}
		token := "rotated"
		if _, err := store.memoryStore.Apply(t.Context(), Update{PrivateAPIToken: &token}, Snapshot{}); err != nil {
			t.Fatal(err)
		}
		close(store.unblock)
		got := <-read
		if got.err != nil || got.snapshot.PrivateAPIToken != token || len(store.entered) != 1 {
			t.Fatalf("credential check missed its fresh store read, version=%d err=%v", got.snapshot.Version, got.err)
		}
	})
}

func TestCredentialRefreshRefusesRegressedVersion(t *testing.T) {
	store := &memoryStore{snapshot: Snapshot{PrivateAPIToken: "older", Version: 2}, found: true}
	service := New(Snapshot{PrivateAPIToken: "cached", Version: 3}, store)
	got, err := service.CurrentForAuthentication(t.Context())
	if !errors.Is(err, errRuntimeConfigVersionRegressed) || got.PrivateAPIToken != "" {
		t.Fatal("a regressed credential read returned a token")
	}
}

type delayedRuntimePublicationStore struct {
	memoryStore
	committed chan struct{}
	unblock   chan struct{}
}

func (s *delayedRuntimePublicationStore) Apply(ctx context.Context, u Update, fallback Snapshot) (Snapshot, error) {
	snapshot, err := s.memoryStore.Apply(ctx, u, fallback)
	close(s.committed)
	select {
	case <-s.unblock:
		return snapshot, err
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	}
}

func TestDelayedLocalUpdateCannotReplaceNewerDistributedSnapshot(t *testing.T) {
	store := &delayedRuntimePublicationStore{
		memoryStore: memoryStore{snapshot: Snapshot{PrivateAPIToken: "initial"}, found: true},
		committed:   make(chan struct{}),
		unblock:     make(chan struct{}),
	}
	release := sync.OnceFunc(func() { close(store.unblock) })
	defer release()
	service := New(Snapshot{}, store)
	service.timeout = 3 * time.Second
	updated := make(chan error, 1)
	go func() {
		token := "local"
		updated <- service.Update(t.Context(), Update{PrivateAPIToken: &token})
	}()
	select {
	case <-store.committed:
	case <-time.After(time.Second):
		t.Fatal("local update did not commit")
	}
	remote := "remote"
	if _, err := store.memoryStore.Apply(t.Context(), Update{PrivateAPIToken: &remote}, Snapshot{}); err != nil {
		t.Fatal(err)
	}
	current, err := service.Current(t.Context())
	if err != nil || current.PrivateAPIToken != remote || current.Version != 2 {
		t.Fatal("read missed the remote rotation")
	}
	release()
	if err := <-updated; err != nil {
		t.Fatal(err)
	}
	if service.localSnapshot().PrivateAPIToken != remote {
		t.Fatal("delayed local publication replaced the remote rotation")
	}
}

package api

import (
	"context"
	"errors"
	"sync"
	"testing"

	platformRuntimeConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/runtimeconfig"
)

// switchableStore is a runtime-config store whose persisted snapshot and load
// failure can be changed between calls, standing in for another instance's writes.
type switchableStore struct {
	mu       sync.Mutex
	snapshot platformRuntimeConfig.Snapshot
	found    bool
	loadErr  error
}

func (s *switchableStore) Load(context.Context) (platformRuntimeConfig.Snapshot, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot, s.found, s.loadErr
}

func (s *switchableStore) Apply(_ context.Context, update platformRuntimeConfig.Update, fallback platformRuntimeConfig.Snapshot) (platformRuntimeConfig.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := fallback
	if s.found {
		next = s.snapshot
	}
	if update.OAuth2DeviceFlowEnabled != nil {
		enabled := *update.OAuth2DeviceFlowEnabled
		next.OAuth2DeviceFlowEnabled = &enabled
	}
	if update.PrivateAPIToken != nil {
		next.PrivateAPIToken = *update.PrivateAPIToken
	}
	s.snapshot, s.found = next, true
	return next, nil
}

func (s *switchableStore) set(snapshot platformRuntimeConfig.Snapshot, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot, s.found, s.loadErr = snapshot, true, err
}

func TestCurrentRuntimeConfigAppliesDistributedSnapshot(t *testing.T) {
	store := &switchableStore{}
	helper := &HarukiToolboxRouterHelpers{
		RuntimeConfig: platformRuntimeConfig.New(platformRuntimeConfig.Snapshot{PrivateAPIToken: "seed"}, store),
	}

	snapshot, err := helper.CurrentRuntimeConfig(t.Context())
	if err != nil || snapshot.PrivateAPIToken != "seed" {
		t.Fatalf("seed snapshot = %+v, %v", snapshot, err)
	}

	store.set(platformRuntimeConfig.Snapshot{PrivateAPIToken: "rotated", AllowedKeys: []string{"k"}}, nil)
	snapshot, err = helper.CurrentRuntimeConfig(t.Context())
	if err != nil || snapshot.PrivateAPIToken != "rotated" {
		t.Fatalf("distributed snapshot = %+v, %v", snapshot, err)
	}
	if helper.PrivateAPIToken != "rotated" {
		t.Fatalf("legacy field not refreshed: %q", helper.PrivateAPIToken)
	}
	if keys := helper.GetAllowedKeys(); len(keys) != 1 || keys[0] != "k" {
		t.Fatalf("legacy allowed keys not refreshed: %#v", keys)
	}
}

func TestCurrentRuntimeConfigFailsClosedOnStoreError(t *testing.T) {
	store := &switchableStore{}
	helper := &HarukiToolboxRouterHelpers{
		RuntimeConfig: platformRuntimeConfig.New(platformRuntimeConfig.Snapshot{PrivateAPIToken: "seed"}, store),
	}
	if _, err := helper.CurrentRuntimeConfig(t.Context()); err != nil {
		t.Fatal(err)
	}

	loadErr := errors.New("redis unavailable")
	store.set(platformRuntimeConfig.Snapshot{PrivateAPIToken: "must-not-apply"}, loadErr)
	snapshot, err := helper.CurrentRuntimeConfig(t.Context())
	if !errors.Is(err, loadErr) {
		t.Fatalf("expected store error, got %v", err)
	}
	if snapshot.PrivateAPIToken != "seed" {
		t.Fatalf("store failure must return the last usable snapshot, got %q", snapshot.PrivateAPIToken)
	}
	if helper.PrivateAPIToken != "seed" {
		t.Fatalf("store failure must not overwrite legacy fields, got %q", helper.PrivateAPIToken)
	}
}

func TestCurrentRuntimeConfigWithoutServiceUsesLegacyFields(t *testing.T) {
	var nilHelper *HarukiToolboxRouterHelpers
	if snapshot, err := nilHelper.CurrentRuntimeConfig(t.Context()); err != nil || snapshot.PrivateAPIToken != "" {
		t.Fatalf("nil helper = %+v, %v", snapshot, err)
	}

	helper := &HarukiToolboxRouterHelpers{PrivateAPIToken: "legacy"}
	snapshot, err := helper.CurrentRuntimeConfig(t.Context())
	if err != nil || snapshot.PrivateAPIToken != "legacy" {
		t.Fatalf("legacy snapshot = %+v, %v", snapshot, err)
	}
}

func TestOAuth2DeviceFlowSwitchDefaultsOff(t *testing.T) {
	var nilHelper *HarukiToolboxRouterHelpers
	if nilHelper.GetOAuth2DeviceFlowEnabled() {
		t.Fatal("nil helper must report the device flow as off")
	}

	store := &switchableStore{}
	helper := &HarukiToolboxRouterHelpers{
		RuntimeConfig: platformRuntimeConfig.New(platformRuntimeConfig.Snapshot{}, store),
	}
	if helper.GetOAuth2DeviceFlowEnabled() {
		t.Fatal("missing switch must be off")
	}

	enabled := true
	if err := helper.UpdateRuntimeConfig(RuntimeConfigUpdate{OAuth2DeviceFlowEnabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if !helper.GetOAuth2DeviceFlowEnabled() {
		t.Fatal("switch update not visible")
	}

	// Another instance turning it off is picked up on the next read.
	disabled := false
	store.set(platformRuntimeConfig.Snapshot{OAuth2DeviceFlowEnabled: &disabled}, nil)
	if helper.GetOAuth2DeviceFlowEnabled() {
		t.Fatal("distributed switch-off ignored")
	}

	// A snapshot written without the field (older binary) keeps the switch off.
	store.set(platformRuntimeConfig.Snapshot{PrivateAPIToken: "older-writer"}, nil)
	if helper.GetOAuth2DeviceFlowEnabled() {
		t.Fatal("snapshot without the field must read as off")
	}
}

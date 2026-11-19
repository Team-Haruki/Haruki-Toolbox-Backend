package runtimeconfig

import (
	"context"
	"errors"
	"sync"
	"time"
)

const defaultStoreTimeout = 500 * time.Millisecond
const maxConcurrentRefreshes = 16

// ErrRefreshBusy reports that Current returned its last published snapshot.
var ErrRefreshBusy = errors.New("runtime configuration refresh is busy")
var errRuntimeConfigVersionRegressed = errors.New("runtime configuration version regressed")

// Update describes a partial runtime configuration update. Nil fields retain
// their current values.
type Update struct {
	AllowedKeys         *[]string
	PrivateAPIToken     *string
	PrivateAPIUserAgent *string
	WebhookJWTSecret    *string
	WebhookEnabled      *bool
	// OAuth2DeviceFlowEnabled is the runtime switch of the device flow.
	OAuth2DeviceFlowEnabled *bool
}

// Snapshot is the persisted runtime configuration contract. Its JSON field
// names match the legacy Redis payload imported during the offline cutover.
type Snapshot struct {
	Version uint64 `json:"_version,omitzero"`
	// AllowedKeys keeps the persisted name `publicApiAllowedKeys`.
	AllowedKeys         []string `json:"publicApiAllowedKeys"`
	PrivateAPIToken     string   `json:"privateApiToken"`
	PrivateAPIUserAgent string   `json:"privateApiUserAgent"`
	WebhookJWTSecret    string   `json:"webhookJwtSecret"`
	WebhookEnabled      *bool    `json:"webhookEnabled,omitzero"`
	// OAuth2DeviceFlowEnabled nil means OFF, unlike WebhookEnabled: the switch
	// fails closed when the field is lost (a seed snapshot from startup config,
	// a write by an older binary, Redis losing a recent PUT false), so a switch
	// turned off during an incident is never silently turned back on.
	OAuth2DeviceFlowEnabled *bool `json:"oauth2DeviceFlowEnabled,omitzero"`
}

// Store distributes mutable settings between backend instances.
type Store interface {
	Load(ctx context.Context) (Snapshot, bool, error)
	// Apply atomically merges update into the persisted snapshot and returns
	// the value that was committed. When no persisted snapshot exists,
	// fallback is used as the initial value.
	Apply(ctx context.Context, update Update, fallback Snapshot) (Snapshot, error)
}

// Service owns mutable runtime settings. Startup configuration remains
// immutable and is only used to seed this service at the composition root.
type Service struct {
	writeMu sync.Mutex
	mu      sync.RWMutex
	current Snapshot
	store   Store
	timeout time.Duration
	readers chan struct{}
}

func New(initial Snapshot, store Store) *Service {
	return &Service{
		current: cloneSnapshot(initial),
		store:   store,
		timeout: defaultStoreTimeout,
		readers: make(chan struct{}, maxConcurrentRefreshes),
	}
}

// Current returns the latest distributed snapshot. A store failure leaves the
// last usable in-process snapshot intact and is returned to the caller.
func (s *Service) Current(ctx context.Context) (Snapshot, error) {
	return s.readCurrent(ctx, false)
}

// CurrentForAuthentication waits within the store deadline for its own read.
// An unavailable store never supplies credentials from the local snapshot.
func (s *Service) CurrentForAuthentication(ctx context.Context) (Snapshot, error) {
	snapshot, err := s.readCurrent(ctx, true)
	if err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (s *Service) readCurrent(ctx context.Context, waitForSlot bool) (Snapshot, error) {
	if s == nil {
		return Snapshot{}, nil
	}
	if s.store == nil {
		return s.localSnapshot(), nil
	}
	wait, cancel := s.storeContext(ctx)
	defer cancel()
	if err := wait.Err(); err != nil {
		return s.localSnapshot(), err
	}
	if waitForSlot {
		select {
		case s.readers <- struct{}{}:
		case <-wait.Done():
			return Snapshot{}, wait.Err()
		}
	} else {
		select {
		case s.readers <- struct{}{}:
		default:
			return s.localSnapshot(), ErrRefreshBusy
		}
	}
	defer func() { <-s.readers }()
	if err := wait.Err(); err != nil {
		return s.localSnapshot(), err
	}
	// Start a fresh store read for the request so an earlier lookup cannot
	// authorize a request that began after a rotation on another replica.
	knownVersion := s.localVersion()
	latest, found, err := s.store.Load(wait)
	if err != nil {
		return s.localSnapshot(), err
	}
	if !found {
		if waitForSlot && (knownVersion > 0 || latest.Version > 0) {
			return Snapshot{}, errMissingRuntimeConfiguration
		}
		return s.localSnapshot(), nil
	}
	if waitForSlot && latest.Version < knownVersion {
		return Snapshot{}, errRuntimeConfigVersionRegressed
	}
	return s.publish(latest), nil
}

// Update applies and persists a partial update before publishing it locally.
// This retains the previous fail-closed behavior: when persistence fails, the
// in-process snapshot is not changed.
func (s *Service) Update(ctx context.Context, update Update) error {
	if s == nil {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	next := s.localSnapshot()
	if s.store != nil {
		storeCtx, cancel := s.storeContext(ctx)
		committed, err := s.store.Apply(storeCtx, update, next)
		cancel()
		if err != nil {
			return err
		}
		next = committed
	} else {
		applyUpdate(&next, update)
	}
	s.publish(next)
	return nil
}

func (s *Service) publish(snapshot Snapshot) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if snapshot.Version >= s.current.Version {
		s.current = cloneSnapshot(snapshot)
	}
	return cloneSnapshot(s.current)
}

func (s *Service) storeContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	timeout := s.timeout
	if timeout <= 0 {
		timeout = defaultStoreTimeout
	}
	return context.WithTimeout(parent, timeout)
}

func (s *Service) localSnapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSnapshot(s.current)
}

func (s *Service) localVersion() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current.Version
}

func applyUpdate(snapshot *Snapshot, update Update) {
	if update.AllowedKeys != nil {
		snapshot.AllowedKeys = append([]string(nil), (*update.AllowedKeys)...)
	}
	if update.PrivateAPIToken != nil {
		snapshot.PrivateAPIToken = *update.PrivateAPIToken
	}
	if update.PrivateAPIUserAgent != nil {
		snapshot.PrivateAPIUserAgent = *update.PrivateAPIUserAgent
	}
	if update.WebhookJWTSecret != nil {
		snapshot.WebhookJWTSecret = *update.WebhookJWTSecret
	}
	if update.WebhookEnabled != nil {
		enabled := *update.WebhookEnabled
		snapshot.WebhookEnabled = &enabled
	}
	if update.OAuth2DeviceFlowEnabled != nil {
		enabled := *update.OAuth2DeviceFlowEnabled
		snapshot.OAuth2DeviceFlowEnabled = &enabled
	}
}

// OAuth2DeviceFlowSwitchOn reports the runtime device-flow switch; a missing
// field is off.
func (s Snapshot) OAuth2DeviceFlowSwitchOn() bool {
	return s.OAuth2DeviceFlowEnabled != nil && *s.OAuth2DeviceFlowEnabled
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	snapshot.AllowedKeys = append([]string(nil), snapshot.AllowedKeys...)
	if snapshot.WebhookEnabled != nil {
		enabled := *snapshot.WebhookEnabled
		snapshot.WebhookEnabled = &enabled
	}
	if snapshot.OAuth2DeviceFlowEnabled != nil {
		enabled := *snapshot.OAuth2DeviceFlowEnabled
		snapshot.OAuth2DeviceFlowEnabled = &enabled
	}
	return snapshot
}

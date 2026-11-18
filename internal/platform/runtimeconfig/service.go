package runtimeconfig

import (
	"context"
	"errors"
	"sync"
	"time"
)

const defaultStoreTimeout = 500 * time.Millisecond
const maxConcurrentRefreshes = 16

var errRefreshBusy = errors.New("runtime configuration refresh is busy")

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
	writeMu      sync.Mutex
	mu           sync.RWMutex
	current      Snapshot
	revision     uint64
	store        Store
	timeout      time.Duration
	readSequence uint64
	appliedRead  uint64
	readers      chan struct{}
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
	select {
	case s.readers <- struct{}{}:
		defer func() { <-s.readers }()
	default:
		return s.localSnapshot(), errRefreshBusy
	}
	// Start a fresh store read for the request so an earlier lookup cannot
	// authorize a request that began after a rotation on another replica.
	s.mu.Lock()
	revision := s.revision
	s.readSequence++
	sequence := s.readSequence
	s.mu.Unlock()
	latest, found, err := s.store.Load(wait)
	if err != nil || !found {
		return s.localSnapshot(), err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revision != revision {
		return cloneSnapshot(s.current), nil
	}
	if sequence > s.appliedRead {
		s.current = cloneSnapshot(latest)
		s.appliedRead = sequence
	}
	return cloneSnapshot(latest), nil
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
	s.mu.Lock()
	s.current = cloneSnapshot(next)
	s.revision++
	s.mu.Unlock()
	return nil
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

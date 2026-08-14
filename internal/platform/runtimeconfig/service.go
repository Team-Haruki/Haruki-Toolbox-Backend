package runtimeconfig

import (
	"context"
	"sync"
	"time"
)

const defaultStoreTimeout = 500 * time.Millisecond

// Update describes a partial runtime configuration update. Nil fields retain
// their current values.
type Update struct {
	PublicAPIAllowedKeys *[]string
	PrivateAPIToken      *string
	PrivateAPIUserAgent  *string
	HarukiProxyUserAgent *string
	HarukiProxyVersion   *string
	HarukiProxySecret    *string
	HarukiProxyUnpackKey *string
	WebhookJWTSecret     *string
	WebhookEnabled       *bool
}

// Snapshot is the persisted runtime configuration contract. Its JSON field
// names intentionally match the existing Redis payload so extracting this
// service does not invalidate settings written by older backend instances.
type Snapshot struct {
	PublicAPIAllowedKeys []string `json:"publicApiAllowedKeys"`
	PrivateAPIToken      string   `json:"privateApiToken"`
	PrivateAPIUserAgent  string   `json:"privateApiUserAgent"`
	HarukiProxyUserAgent string   `json:"harukiProxyUserAgent"`
	HarukiProxyVersion   string   `json:"harukiProxyVersion"`
	HarukiProxySecret    string   `json:"harukiProxySecret"`
	HarukiProxyUnpackKey string   `json:"harukiProxyUnpackKey"`
	WebhookJWTSecret     string   `json:"webhookJwtSecret"`
	WebhookEnabled       *bool    `json:"webhookEnabled,omitempty"`
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
	// operationMu keeps a single instance from publishing committed snapshots
	// out of order when its Current and Update methods run concurrently.
	operationMu sync.Mutex
	mu          sync.RWMutex
	current     Snapshot
	store       Store
	timeout     time.Duration
}

func New(initial Snapshot, store Store) *Service {
	return &Service{
		current: cloneSnapshot(initial),
		store:   store,
		timeout: defaultStoreTimeout,
	}
}

// Current returns the latest distributed snapshot. A store failure leaves the
// last usable in-process snapshot intact and is returned to the caller.
func (s *Service) Current(ctx context.Context) (Snapshot, error) {
	if s == nil {
		return Snapshot{}, nil
	}
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	if err := s.refresh(ctx); err != nil {
		return s.localSnapshot(), err
	}
	return s.localSnapshot(), nil
}

// Update applies and persists a partial update before publishing it locally.
// This retains the previous fail-closed behavior: when persistence fails, the
// in-process snapshot is not changed.
func (s *Service) Update(ctx context.Context, update Update) error {
	if s == nil {
		return nil
	}
	s.operationMu.Lock()
	defer s.operationMu.Unlock()

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
	s.setLocalSnapshot(next)
	return nil
}

func (s *Service) refresh(ctx context.Context) error {
	if s == nil || s.store == nil {
		return nil
	}
	storeCtx, cancel := s.storeContext(ctx)
	latest, found, err := s.store.Load(storeCtx)
	cancel()
	if err != nil {
		return err
	}
	if found {
		s.setLocalSnapshot(latest)
	}
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

func (s *Service) setLocalSnapshot(snapshot Snapshot) {
	s.mu.Lock()
	s.current = cloneSnapshot(snapshot)
	s.mu.Unlock()
}

func applyUpdate(snapshot *Snapshot, update Update) {
	if update.PublicAPIAllowedKeys != nil {
		snapshot.PublicAPIAllowedKeys = append([]string(nil), (*update.PublicAPIAllowedKeys)...)
	}
	if update.PrivateAPIToken != nil {
		snapshot.PrivateAPIToken = *update.PrivateAPIToken
	}
	if update.PrivateAPIUserAgent != nil {
		snapshot.PrivateAPIUserAgent = *update.PrivateAPIUserAgent
	}
	if update.HarukiProxyUserAgent != nil {
		snapshot.HarukiProxyUserAgent = *update.HarukiProxyUserAgent
	}
	if update.HarukiProxyVersion != nil {
		snapshot.HarukiProxyVersion = *update.HarukiProxyVersion
	}
	if update.HarukiProxySecret != nil {
		snapshot.HarukiProxySecret = *update.HarukiProxySecret
	}
	if update.HarukiProxyUnpackKey != nil {
		snapshot.HarukiProxyUnpackKey = *update.HarukiProxyUnpackKey
	}
	if update.WebhookJWTSecret != nil {
		snapshot.WebhookJWTSecret = *update.WebhookJWTSecret
	}
	if update.WebhookEnabled != nil {
		enabled := *update.WebhookEnabled
		snapshot.WebhookEnabled = &enabled
	}
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	snapshot.PublicAPIAllowedKeys = append([]string(nil), snapshot.PublicAPIAllowedKeys...)
	if snapshot.WebhookEnabled != nil {
		enabled := *snapshot.WebhookEnabled
		snapshot.WebhookEnabled = &enabled
	}
	return snapshot
}

package gamedata

import (
	"context"
	"fmt"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/nuverserestore"
)

// Service owns the dedicated PostgreSQL pool and game-data stores.
type Service struct {
	pool    *Pool
	suite   *Store
	mysekai *Store
	limits  Limits
}

// NewService binds a pool to the pinned catalogs.
func NewService(p *Pool) *Service {
	return &Service{
		pool:    p,
		suite:   NewStore(p, catalog.Suite()),
		mysekai: NewStore(p, catalog.Mysekai()),
		limits:  DefaultLimits(),
	}
}

// Suite returns the suite store, or nil.
func (s *Service) Suite() *Store {
	if s == nil {
		return nil
	}
	return s.suite
}

// Mysekai returns the mysekai store, or nil.
func (s *Service) Mysekai() *Store {
	if s == nil {
		return nil
	}
	return s.mysekai
}

// StoreFor returns the store serving a collection name ("suite" / "mysekai").
// Anything that is not suite is mysekai, matching the dispatch the upload path
// has always used.
func (s *Service) StoreFor(collection string) *Store {
	if s == nil {
		return nil
	}
	if collection == "suite" {
		return s.suite
	}
	return s.mysekai
}

// Limits are the upload caps this service enforces.
func (s *Service) Limits() Limits {
	if s == nil {
		return DefaultLimits()
	}
	return s.limits
}

// SetLimits overrides the upload caps.
func (s *Service) SetLimits(l Limits) {
	if s != nil {
		s.limits = l
	}
}

// Pool exposes the underlying pool for schema work and health checks.
func (s *Service) Pool() *Pool {
	if s == nil {
		return nil
	}
	return s.pool
}

// Ping verifies the store is reachable.
func (s *Service) Ping(ctx context.Context) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("game data pool is not initialized")
	}
	return s.pool.Ping(ctx)
}

// Close releases the pool.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	return s.pool.Close()
}

// NewServiceWithRestore shares one immutable policy with upload processing.
func NewServiceWithRestore(p *Pool, restorer *nuverserestore.MysekaiRestorer) *Service {
	s := NewService(p)
	s.suite.restorer = restorer
	s.mysekai.restorer = restorer
	return s
}
func (s *Service) HarvestSchemaFingerprint(server string) string {
	var r *nuverserestore.MysekaiRestorer
	if s != nil && s.mysekai != nil {
		r = s.mysekai.restorer
	}
	return r.Fingerprint(server)
}

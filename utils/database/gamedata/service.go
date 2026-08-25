package gamedata

import (
	"context"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

// Service is the game-data store as the rest of the process sees it: the pool,
// one Store per collection, and the read-source decision.
//
// It is one aggregate rather than three fields on the database manager so the
// cutover does not widen the process-wide service locator, and so "which
// datastore answers reads" is asked in exactly one place.
type Service struct {
	pool             *Pool
	suite            *Store
	mysekai          *Store
	readFromPostgres bool
	limits           Limits
}

// NewService binds a pool to the pinned catalogs.
//
// readFromPostgres is the cutover switch. It is false by default so deploying
// the new code changes nothing: every read keeps going to MongoDB until the flip
// is made deliberately, and the same one-line change reverses it.
func NewService(p *Pool, readFromPostgres bool) *Service {
	return &Service{
		pool:             p,
		suite:            NewStore(p, catalog.Suite()),
		mysekai:          NewStore(p, catalog.Mysekai()),
		readFromPostgres: readFromPostgres,
		limits:           DefaultLimits(),
	}
}

// ReadsFromPostgres reports whether reads should be served from PostgreSQL.
// A nil Service answers false, so a build with no game-data pool configured
// keeps its MongoDB behaviour without every call site nil-checking.
func (s *Service) ReadsFromPostgres() bool { return s != nil && s.readFromPostgres }

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
		return nil
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

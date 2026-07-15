package manager

import (
	"context"
	"fmt"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"time"

	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type MongoDBManager struct {
	client            *mongo.Client
	suiteCollection   *mongo.Collection
	mysekaiCollection *mongo.Collection
}

func (m *MongoDBManager) Ping(ctx context.Context) error {
	if m == nil || m.client == nil {
		return fmt.Errorf("mongo client is nil")
	}
	return m.client.Ping(ctx, nil)
}

func (m *MongoDBManager) Disconnect(ctx context.Context) error {
	if m == nil || m.client == nil {
		return nil
	}
	return m.client.Disconnect(ctx)
}

// mongoOptions holds optional construction tunables applied by MongoOption funcs.
type mongoOptions struct {
	poolMonitor *event.PoolMonitor
}

// MongoOption customizes NewMongoDBManager. The variadic form keeps existing
// call sites (e.g. the backfill CLI) source-compatible.
type MongoOption func(*mongoOptions)

// WithPoolMonitor attaches a driver PoolMonitor so connection-pool telemetry can be
// sampled. Pass PoolStats.Monitor(). Nil monitors are ignored.
func WithPoolMonitor(m *event.PoolMonitor) MongoOption {
	return func(o *mongoOptions) {
		o.poolMonitor = m
	}
}

func NewMongoDBManager(
	ctx context.Context,
	dbURL, db, suite, mysekai string,
	opts ...MongoOption,
) (*MongoDBManager, error) {
	var applied mongoOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&applied)
		}
	}
	// Pool sizing keeps warm connections so bursts of serving reads do not pay
	// the 2-at-a-time handshake penalty, and raises the ceiling above the driver
	// default of 100. ServerSelectionTimeout is lowered from the 30s default so a
	// transient topology/primary issue fails fast instead of parking a request for
	// 30s. NOTE: we intentionally do NOT set a client-level SetTimeout (CSOT) here —
	// that would also bound cursor/backfill lifetime and break long scans. Per-op
	// deadlines (context.WithTimeout in the serving handlers) bound the hot reads.
	clientOpts := options.Client().
		ApplyURI(dbURL).
		SetMaxPoolSize(200).
		SetMinPoolSize(20).
		SetMaxConnecting(10).
		SetServerSelectionTimeout(10 * time.Second)
	if applied.poolMonitor != nil {
		clientOpts.SetPoolMonitor(applied.poolMonitor)
	}
	client, err := mongo.Connect(clientOpts)
	if err != nil {
		harukiLogger.Errorf("Failed to connect to MongoDB: %v", err)
		return nil, err
	}

	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(ctx)
		harukiLogger.Errorf("Failed to ping MongoDB: %v", err)
		return nil, err
	}

	return &MongoDBManager{
		client:            client,
		suiteCollection:   client.Database(db).Collection(suite),
		mysekaiCollection: client.Database(db).Collection(mysekai),
	}, nil
}

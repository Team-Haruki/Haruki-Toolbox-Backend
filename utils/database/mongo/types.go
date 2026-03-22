package manager

import (
	"context"
	"fmt"
	harukiLogger "haruki-suite/utils/logger"

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

func NewMongoDBManager(
	ctx context.Context,
	dbURL, db, suite, mysekai string,
) (*MongoDBManager, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(dbURL))
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

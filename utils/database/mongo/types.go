package manager

import (
	"context"
	harukiLogger "haruki-suite/utils/logger"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type MongoDBManager struct {
	client                *mongo.Client
	suiteCollection       *mongo.Collection
	mysekaiCollection     *mongo.Collection
	webhookCollection     *mongo.Collection
	webhookUserCollection *mongo.Collection
}

func NewMongoDBManager(
	ctx context.Context,
	dbURL, db, suite, mysekai, webhookUser, webhookUserUser string,
) (*MongoDBManager, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(dbURL))
	if err != nil {
		harukiLogger.Errorf("Failed to connect to MongoDB: %v", err)
		return nil, err
	}

	if err := client.Ping(ctx, nil); err != nil {
		harukiLogger.Errorf("Failed to ping MongoDB: %v", err)
		return nil, err
	}

	return &MongoDBManager{
		client:                client,
		suiteCollection:       client.Database(db).Collection(suite),
		mysekaiCollection:     client.Database(db).Collection(mysekai),
		webhookCollection:     client.Database(db).Collection(webhookUser),
		webhookUserCollection: client.Database(db).Collection(webhookUserUser),
	}, nil
}

package manager

import (
	"context"
	harukiLogger "haruki-suite/utils/logger"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func closeCursor(ctx context.Context, cursor *mongo.Cursor) {
	_ = cursor.Close(ctx)
}

func decodeBsonMResults(ctx context.Context, cursor *mongo.Cursor, label string) ([]bson.M, error) {
	var results []bson.M
	if err := cursor.All(ctx, &results); err != nil {
		harukiLogger.Errorf("Failed to decode %s: %v", label, err)
		return nil, err
	}
	return results, nil
}

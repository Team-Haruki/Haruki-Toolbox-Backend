package manager

import (
	"context"
	"errors"
	harukiLogger "haruki-suite/utils/logger"
	"strconv"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (m *MongoDBManager) GetWebhookUser(ctx context.Context, id, credential string) (bson.M, error) {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		harukiLogger.Errorf("Invalid webhook ID format: %v", err)
		return nil, err
	}

	var result bson.M
	err = m.webhookCollection.FindOne(
		ctx,
		bson.M{fieldID: oid, fieldCredential: credential},
		options.FindOne().SetProjection(bson.M{fieldCallbackURL: 1, fieldCredential: 1, fieldID: 0}),
	).Decode(&result)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		harukiLogger.Errorf("Failed to get webhook user %s: %v", id, err)
	}
	return result, err
}

func (m *MongoDBManager) GetWebhookPushAPI(
	ctx context.Context,
	userID int64,
	server, dataType string,
) ([]bson.M, error) {
	var binding bson.M
	err := m.webhookUserCollection.FindOne(
		ctx,
		bson.M{fieldUID: strconv.FormatInt(userID, 10), fieldServer: server, fieldType: dataType},
	).Decode(&binding)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return []bson.M{}, nil
	}
	if err != nil {
		harukiLogger.Errorf("Failed to get webhook binding for user %d: %v", userID, err)
		return nil, err
	}

	webhookIDs, ok := binding[fieldWebhookUserIDs].(bson.A)
	if !ok {
		return []bson.M{}, nil
	}
	ids := collectObjectIDs(webhookIDs)
	if len(ids) == 0 {
		return []bson.M{}, nil
	}

	filter := bson.M{fieldID: bson.M{"$in": ids}}
	cursor, err := m.webhookCollection.Find(
		ctx,
		filter,
		options.Find().SetProjection(bson.M{fieldCallbackURL: 1, fieldBearer: 1, fieldID: 0}),
	)
	if err != nil {
		harukiLogger.Errorf("Failed to find webhooks: %v", err)
		return nil, err
	}
	defer closeCursor(ctx, cursor)

	return decodeBsonMResults(ctx, cursor, "webhooks")
}

func collectObjectIDs(rawIDs bson.A) []bson.ObjectID {
	ids := make([]bson.ObjectID, 0, len(rawIDs))
	for _, v := range rawIDs {
		switch val := v.(type) {
		case string:
			if oid, err := bson.ObjectIDFromHex(val); err == nil {
				ids = append(ids, oid)
			}
		case bson.ObjectID:
			ids = append(ids, val)
		}
	}
	return ids
}

func (m *MongoDBManager) AddWebhookPushUser(
	ctx context.Context,
	userID string,
	server, dataType, webhookID string,
) error {
	_, err := m.webhookUserCollection.UpdateOne(
		ctx,
		bson.M{fieldUID: userID, fieldServer: server, fieldType: dataType},
		bson.M{"$addToSet": bson.M{fieldWebhookUserIDs: webhookID}},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		harukiLogger.Errorf("Failed to add webhook push user: %v", err)
	}
	return err
}

func (m *MongoDBManager) RemoveWebhookPushUser(
	ctx context.Context,
	userID string,
	server, dataType, webhookID string,
) error {
	_, err := m.webhookUserCollection.UpdateOne(
		ctx,
		bson.M{fieldUID: userID, fieldServer: server, fieldType: dataType},
		bson.M{"$pull": bson.M{fieldWebhookUserIDs: webhookID}},
	)
	if err != nil {
		harukiLogger.Errorf("Failed to remove webhook push user: %v", err)
	}
	return err
}

func (m *MongoDBManager) GetWebhookSubscribers(ctx context.Context, webhookID string) ([]bson.M, error) {
	cursor, err := m.webhookUserCollection.Find(
		ctx,
		bson.M{fieldWebhookUserIDs: webhookID},
		options.Find().SetProjection(bson.M{fieldUID: 1, fieldServer: 1, fieldType: 1, fieldID: 0}),
	)
	if err != nil {
		harukiLogger.Errorf("Failed to find subscribers for webhook %s: %v", webhookID, err)
		return nil, err
	}
	defer closeCursor(ctx, cursor)

	return decodeBsonMResults(ctx, cursor, "subscribers")
}

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

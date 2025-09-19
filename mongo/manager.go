package manager

import (
	"context"
	"errors"
	"fmt"
	"haruki-suite/utils"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoDBManager struct {
	client                *mongo.Client
	suiteCollection       *mongo.Collection
	mysekaiCollection     *mongo.Collection
	webhookCollection     *mongo.Collection
	webhookUserCollection *mongo.Collection
}

func NewMongoDBManager(ctx context.Context, dbURL, db, suite, mysekai, webhookUser, webhookUserUser string) (*MongoDBManager, error) {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(dbURL))
	if err != nil {
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

func (m *MongoDBManager) UpdateData(ctx context.Context, userID int, data map[string]interface{}, dataType utils.UploadDataType) (*mongo.UpdateResult, error) {
	var collection *mongo.Collection
	if dataType == utils.UploadDataTypeSuite {
		collection = m.suiteCollection
	} else {
		collection = m.mysekaiCollection
	}

	updateDoc := bson.M{}
	setDoc := bson.M{}
	addToSetDoc := bson.M{}

	for key, value := range data {
		if key == "userEvents" || key == "userWorldBlooms" {
			if arr, ok := value.([]interface{}); ok {
				addToSetDoc[key] = bson.M{"$each": arr}
			}
		} else {
			setDoc[key] = value
		}
	}

	if len(setDoc) > 0 {
		updateDoc["$set"] = setDoc
	}
	if len(addToSetDoc) > 0 {
		updateDoc["$addToSet"] = addToSetDoc
	}

	return collection.UpdateOne(ctx,
		bson.M{"_id": userID},
		updateDoc,
		options.Update().SetUpsert(true),
	)
}

func (m *MongoDBManager) GetData(ctx context.Context, userID int, server string, dataType utils.UploadDataType) (bson.M, error) {
	var collection *mongo.Collection
	if dataType == utils.UploadDataTypeSuite {
		collection = m.suiteCollection
	} else {
		collection = m.mysekaiCollection
	}

	var result bson.M
	err := collection.FindOne(ctx, bson.M{"_id": userID, "server": server}).Decode(&result)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	return result, err
}

func (m *MongoDBManager) GetWebhookUser(ctx context.Context, id, credential string) (bson.M, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, err
	}
	var result bson.M
	err = m.webhookCollection.FindOne(ctx,
		bson.M{"_id": oid, "credential": credential},
		options.FindOne().SetProjection(bson.M{"callback_url": 1, "credential": 1, "_id": 0}),
	).Decode(&result)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	return result, err
}

func (m *MongoDBManager) GetWebhookPushAPI(ctx context.Context, userID int, server, dataType string) ([]bson.M, error) {
	var binding bson.M
	err := m.webhookUserCollection.FindOne(ctx,
		bson.M{"uid": fmt.Sprintf("%d", userID), "server": server, "type": dataType},
	).Decode(&binding)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return []bson.M{}, nil
	}
	if err != nil {
		return nil, err
	}

	webhookIDs, ok := binding["webhook_user_ids"].(primitive.A)
	if !ok {
		return []bson.M{}, nil
	}

	var ids []primitive.ObjectID
	for _, v := range webhookIDs {
		if s, ok := v.(string); ok {
			if oid, err := primitive.ObjectIDFromHex(s); err == nil {
				ids = append(ids, oid)
			}
		}
	}

	filter := bson.M{"_id": bson.M{"$in": ids}}
	cursor, err := m.webhookCollection.Find(ctx, filter,
		options.Find().SetProjection(bson.M{"callback_url": 1, "bearer": 1, "_id": 0}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var results []bson.M
	if err := cursor.All(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (m *MongoDBManager) AddWebhookPushUser(ctx context.Context, userID int, server, dataType, webhookID string) error {
	_, err := m.webhookUserCollection.UpdateOne(ctx,
		bson.M{"uid": fmt.Sprintf("%d", userID), "server": server, "type": dataType},
		bson.M{"$addToSet": bson.M{"webhook_user_ids": webhookID}},
		options.Update().SetUpsert(true),
	)
	return err
}

func (m *MongoDBManager) RemoveWebhookPushUser(ctx context.Context, userID int, server, dataType, webhookID string) error {
	_, err := m.webhookUserCollection.UpdateOne(ctx,
		bson.M{"uid": fmt.Sprintf("%d", userID), "server": server, "type": dataType},
		bson.M{"$pull": bson.M{"webhook_user_ids": webhookID}},
	)
	return err
}

func (m *MongoDBManager) GetWebhookSubscribers(ctx context.Context, webhookID string) ([]bson.M, error) {
	cursor, err := m.webhookUserCollection.Find(ctx,
		bson.M{"webhook_user_ids": webhookID},
		options.Find().SetProjection(bson.M{"uid": 1, "server": 1, "type": 1, "_id": 0}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var results []bson.M
	if err := cursor.All(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (m *MongoDBManager) SearchPutMysekaiFixtureUser(ctx context.Context, server string, fixtureID int) ([]bson.M, error) {
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"server": server, "policy": "private"}}},
		{{Key: "$unwind", Value: "$updatedResources.userMysekaiSiteHousingLayouts"}},
		{{Key: "$unwind", Value: "$updatedResources.userMysekaiSiteHousingLayouts.mysekaiSiteHousingLayouts"}},
		{{Key: "$unwind", Value: "$updatedResources.userMysekaiSiteHousingLayouts.mysekaiSiteHousingLayouts.mysekaiFixtures"}},
		{{Key: "$match", Value: bson.M{
			"updatedResources.userMysekaiSiteHousingLayouts.mysekaiSiteHousingLayouts.mysekaiFixtures.mysekaiFixtureId": fixtureID,
		}}},
		{{Key: "$group", Value: bson.M{
			"_id":            "$_id",
			"mysekaiSiteIds": bson.M{"$addToSet": "$updatedResources.userMysekaiSiteHousingLayouts.mysekaiSiteId"},
		}}},
	}

	cursor, err := m.mysekaiCollection.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var results []bson.M
	if err := cursor.All(ctx, &results); err != nil {
		return nil, err
	}
	return results, nil
}

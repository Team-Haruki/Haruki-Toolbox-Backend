package manager

import (
	"context"
	"errors"
	"haruki-suite/utils"
	"strconv"

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

func getInt(m map[string]interface{}, key string) int64 {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case int:
			return int64(n)
		case int32:
			return int64(n)
		case int64:
			return n
		case float64:
			return int64(n)
		}
	}
	return 0
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

func (m *MongoDBManager) UpdateData(ctx context.Context, server string, userID int64, data map[string]interface{}, dataType utils.UploadDataType) (*mongo.UpdateResult, error) {
	var collection *mongo.Collection
	if dataType == utils.UploadDataTypeSuite {
		collection = m.suiteCollection
	} else {
		collection = m.mysekaiCollection
	}

	var oldData map[string]interface{}
	err := collection.FindOne(ctx, bson.M{"_id": userID}).Decode(&oldData)
	if errors.Is(err, mongo.ErrNoDocuments) {
		oldData = make(map[string]interface{})
	} else if err != nil {
		return nil, err
	}

	finalData := bson.M{}

	oldEvents, _ := oldData["userEvents"].(primitive.A)
	newEvents, _ := data["userEvents"].([]interface{})
	allEvents := append(oldEvents, newEvents...)

	latestEvents := make(map[int64]map[string]interface{})
	for _, ev := range allEvents {
		if e, ok := ev.(map[string]interface{}); ok {
			eventID := getInt(e, "eventId")
			if old, exists := latestEvents[eventID]; !exists {
				latestEvents[eventID] = e
			} else {
				newPoint := getInt(e, "eventPoint")
				oldPoint := getInt(old, "eventPoint")
				if newPoint > oldPoint {
					latestEvents[eventID] = e
				} else if newPoint == oldPoint {
					if len(e) > len(old) {
						latestEvents[eventID] = e
					}
				}
			}
		}
	}
	if len(latestEvents) > 0 {
		arr := make([]interface{}, 0, len(latestEvents))
		for _, v := range latestEvents {
			arr = append(arr, v)
		}
		finalData["userEvents"] = arr
	}

	oldBlooms, _ := oldData["userWorldBlooms"].(primitive.A)
	newBlooms, _ := data["userWorldBlooms"].([]interface{})
	allBlooms := append(oldBlooms, newBlooms...)

	type bloomKey struct {
		EventID, CharID int64
	}
	latestBlooms := make(map[bloomKey]map[string]interface{})
	for _, bv := range allBlooms {
		if b, ok := bv.(map[string]interface{}); ok {
			key := bloomKey{
				EventID: getInt(b, "eventId"),
				CharID:  getInt(b, "gameCharacterId"),
			}
			if old, exists := latestBlooms[key]; !exists {
				latestBlooms[key] = b
			} else {
				newPoint := getInt(b, "worldBloomChapterPoint")
				oldPoint := getInt(old, "worldBloomChapterPoint")
				if newPoint > oldPoint {
					latestBlooms[key] = b
				} else if newPoint == oldPoint {
					if len(b) > len(old) {
						latestBlooms[key] = b
					}
				}
			}
		}
	}
	if len(latestBlooms) > 0 {
		arr := make([]interface{}, 0, len(latestBlooms))
		for _, v := range latestBlooms {
			arr = append(arr, v)
		}
		finalData["userWorldBlooms"] = arr
	}

	for key, value := range data {
		if key != "userEvents" && key != "userWorldBlooms" {
			finalData[key] = value
		}
	}

	updateDoc := bson.M{"$set": finalData}
	return collection.UpdateOne(ctx,
		bson.M{"_id": userID, "server": server},
		updateDoc,
		options.Update().SetUpsert(true),
	)
}

func (m *MongoDBManager) GetData(ctx context.Context, userID int64, server string, dataType utils.UploadDataType) (bson.M, error) {
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

func (m *MongoDBManager) GetWebhookPushAPI(ctx context.Context, userID int64, server, dataType string) ([]bson.M, error) {
	var binding bson.M
	err := m.webhookUserCollection.FindOne(ctx,
		bson.M{"uid": strconv.FormatInt(userID, 10), "server": server, "type": dataType},
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
		switch val := v.(type) {
		case string:
			if oid, err := primitive.ObjectIDFromHex(val); err == nil {
				ids = append(ids, oid)
			}
		case primitive.ObjectID:
			ids = append(ids, val)
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

func (m *MongoDBManager) AddWebhookPushUser(ctx context.Context, userID string, server, dataType, webhookID string) error {
	_, err := m.webhookUserCollection.UpdateOne(ctx,
		bson.M{"uid": userID, "server": server, "type": dataType},
		bson.M{"$addToSet": bson.M{"webhook_user_ids": webhookID}},
		options.Update().SetUpsert(true),
	)
	return err
}

func (m *MongoDBManager) RemoveWebhookPushUser(ctx context.Context, userID string, server, dataType, webhookID string) error {
	_, err := m.webhookUserCollection.UpdateOne(ctx,
		bson.M{"uid": userID, "server": server, "type": dataType},
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
		{{Key: "$match", Value: bson.M{"server": server}}},
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

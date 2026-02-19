package manager

import (
	"context"
	"errors"
	"haruki-suite/utils"
	harukiLogger "haruki-suite/utils/logger"
	"strconv"

	"go.mongodb.org/mongo-driver/v2/bson"
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

func (m *MongoDBManager) UpdateData(ctx context.Context, server string, userID int64, data map[string]interface{}, dataType utils.UploadDataType) (*mongo.UpdateResult, error) {
	collection := m.getCollectionByDataType(dataType)
	var updateDoc bson.M
	switch dataType {
	case utils.UploadDataTypeSuite:
		oldData, err := m.fetchOldData(ctx, collection, userID)
		if err != nil {
			return nil, err
		}
		finalData := m.buildFinalData(oldData, data)
		finalData["server"] = server
		updateDoc = bson.M{"$set": finalData}
	case utils.UploadDataTypeMysekai:
		data["server"] = server
		updateDoc = bson.M{"$set": data}
	default:
		updatedResources, _ := data["updatedResources"].(map[string]interface{})
		updateDoc = bson.M{"$set": bson.M{
			"server":      server,
			"upload_time": data["upload_time"],
			"updatedResources.userMysekaiHarvestMaps": updatedResources["userMysekaiHarvestMaps"],
		}}
	}
	res, err := collection.UpdateOne(ctx,
		bson.M{"_id": userID},
		updateDoc,
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		harukiLogger.Errorf("Failed to update data for user %d: %v", userID, err)
	}
	return res, err
}

func (m *MongoDBManager) getCollectionByDataType(dataType utils.UploadDataType) *mongo.Collection {
	if dataType == utils.UploadDataTypeSuite {
		return m.suiteCollection
	}
	return m.mysekaiCollection
}

func (m *MongoDBManager) fetchOldData(ctx context.Context, collection *mongo.Collection, userID int64) (map[string]interface{}, error) {
	var oldData map[string]interface{}
	err := collection.FindOne(ctx, bson.M{"_id": userID}).Decode(&oldData)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return make(map[string]interface{}), nil
	}
	if err != nil {
		harukiLogger.Errorf("Failed to fetch old data for user %d: %v", userID, err)
		return nil, err
	}
	return oldData, nil
}

func (m *MongoDBManager) buildFinalData(oldData, data map[string]interface{}) bson.M {
	finalData := bson.M{}

	if mergedEvents := mergeUserEvents(oldData, data); mergedEvents != nil {
		finalData["userEvents"] = mergedEvents
	}

	if mergedBlooms := mergeWorldBlooms(oldData, data); mergedBlooms != nil {
		finalData["userWorldBlooms"] = mergedBlooms
	}

	for key, value := range data {
		if key != "userEvents" && key != "userWorldBlooms" {
			finalData[key] = value
		}
	}

	return finalData
}

func mergeUserEvents(oldData, newData map[string]interface{}) []interface{} {
	oldEvents, _ := oldData["userEvents"].(bson.A)
	newEvents, _ := newData["userEvents"].([]interface{})
	allEvents := append(oldEvents, newEvents...)

	latestEvents := make(map[int64]map[string]interface{})
	for _, ev := range allEvents {
		if e, ok := ev.(map[string]interface{}); ok {
			eventID := getInt(e, "eventId")
			if old, exists := latestEvents[eventID]; !exists {
				latestEvents[eventID] = e
			} else {
				if shouldReplaceEvent(e, old) {
					latestEvents[eventID] = e
				}
			}
		}
	}

	if len(latestEvents) == 0 {
		return nil
	}

	arr := make([]interface{}, 0, len(latestEvents))
	for _, v := range latestEvents {
		arr = append(arr, v)
	}
	return arr
}

func shouldReplaceEvent(newEvent, oldEvent map[string]interface{}) bool {
	newPoint := getInt(newEvent, "eventPoint")
	oldPoint := getInt(oldEvent, "eventPoint")
	if newPoint >= oldPoint {
		return true
	}
	return false
}

type bloomKey struct {
	EventID, CharID int64
}

func mergeWorldBlooms(oldData, newData map[string]interface{}) []interface{} {
	oldBlooms, _ := oldData["userWorldBlooms"].(bson.A)
	newBlooms, _ := newData["userWorldBlooms"].([]interface{})
	allBlooms := append(oldBlooms, newBlooms...)

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
				if shouldReplaceBloom(b, old) {
					latestBlooms[key] = b
				}
			}
		}
	}

	if len(latestBlooms) == 0 {
		return nil
	}

	arr := make([]interface{}, 0, len(latestBlooms))
	for _, v := range latestBlooms {
		arr = append(arr, v)
	}
	return arr
}

func shouldReplaceBloom(newBloom, oldBloom map[string]interface{}) bool {
	newPoint := getInt(newBloom, "worldBloomChapterPoint")
	oldPoint := getInt(oldBloom, "worldBloomChapterPoint")
	if newPoint >= oldPoint {
		return true
	}
	return false
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
	if err != nil {
		harukiLogger.Errorf("Failed to get data for user %d: %v", userID, err)
	}
	return result, err
}

func (m *MongoDBManager) GetDataWithProjection(ctx context.Context, userID int64, server string, dataType utils.UploadDataType, projection bson.M) (bson.D, error) {
	var collection *mongo.Collection
	if dataType == utils.UploadDataTypeSuite {
		collection = m.suiteCollection
	} else {
		collection = m.mysekaiCollection
	}

	filter := bson.M{"_id": userID, "server": server}
	opts := options.FindOne()
	if projection != nil {
		opts.SetProjection(projection)
	}

	var result bson.D
	err := collection.FindOne(ctx, filter, opts).Decode(&result)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		harukiLogger.Errorf("Failed to get data for user %d: %v", userID, err)
	}
	return result, err
}

func (m *MongoDBManager) GetWebhookUser(ctx context.Context, id, credential string) (bson.M, error) {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		harukiLogger.Errorf("Invalid webhook ID format: %v", err)
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
	if err != nil {
		harukiLogger.Errorf("Failed to get webhook user %s: %v", id, err)
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
		harukiLogger.Errorf("Failed to get webhook binding for user %d: %v", userID, err)
		return nil, err
	}

	webhookIDs, ok := binding["webhook_user_ids"].(bson.A)
	if !ok {
		return []bson.M{}, nil
	}

	var ids []bson.ObjectID
	for _, v := range webhookIDs {
		switch val := v.(type) {
		case string:
			if oid, err := bson.ObjectIDFromHex(val); err == nil {
				ids = append(ids, oid)
			}
		case bson.ObjectID:
			ids = append(ids, val)
		}
	}

	filter := bson.M{"_id": bson.M{"$in": ids}}
	cursor, err := m.webhookCollection.Find(ctx, filter,
		options.Find().SetProjection(bson.M{"callback_url": 1, "bearer": 1, "_id": 0}))
	if err != nil {
		harukiLogger.Errorf("Failed to find webhooks: %v", err)
		return nil, err
	}
	defer func(cursor *mongo.Cursor, ctx context.Context) {
		_ = cursor.Close(ctx)
	}(cursor, ctx)

	var results []bson.M
	if err := cursor.All(ctx, &results); err != nil {
		harukiLogger.Errorf("Failed to decode webhooks: %v", err)
		return nil, err
	}
	return results, nil
}

func (m *MongoDBManager) AddWebhookPushUser(ctx context.Context, userID string, server, dataType, webhookID string) error {
	_, err := m.webhookUserCollection.UpdateOne(ctx,
		bson.M{"uid": userID, "server": server, "type": dataType},
		bson.M{"$addToSet": bson.M{"webhook_user_ids": webhookID}},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		harukiLogger.Errorf("Failed to add webhook push user: %v", err)
	}
	return err
}

func (m *MongoDBManager) RemoveWebhookPushUser(ctx context.Context, userID string, server, dataType, webhookID string) error {
	_, err := m.webhookUserCollection.UpdateOne(ctx,
		bson.M{"uid": userID, "server": server, "type": dataType},
		bson.M{"$pull": bson.M{"webhook_user_ids": webhookID}},
	)
	if err != nil {
		harukiLogger.Errorf("Failed to remove webhook push user: %v", err)
	}
	return err
}

func (m *MongoDBManager) GetWebhookSubscribers(ctx context.Context, webhookID string) ([]bson.M, error) {
	cursor, err := m.webhookUserCollection.Find(ctx,
		bson.M{"webhook_user_ids": webhookID},
		options.Find().SetProjection(bson.M{"uid": 1, "server": 1, "type": 1, "_id": 0}))
	if err != nil {
		harukiLogger.Errorf("Failed to find subscribers for webhook %s: %v", webhookID, err)
		return nil, err
	}
	defer func(cursor *mongo.Cursor, ctx context.Context) {
		_ = cursor.Close(ctx)
	}(cursor, ctx)

	var results []bson.M
	if err := cursor.All(ctx, &results); err != nil {
		harukiLogger.Errorf("Failed to decode subscribers: %v", err)
		return nil, err
	}
	return results, nil
}

func (m *MongoDBManager) SearchPutMysekaiFixtureUser(ctx context.Context, server string, fixtureID int) ([]bson.M, error) {
	pipeline := mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.M{"server": server}}},
		bson.D{{Key: "$unwind", Value: "$updatedResources.userMysekaiSiteHousingLayouts"}},
		bson.D{{Key: "$unwind", Value: "$updatedResources.userMysekaiSiteHousingLayouts.mysekaiSiteHousingLayouts"}},
		bson.D{{Key: "$unwind", Value: "$updatedResources.userMysekaiSiteHousingLayouts.mysekaiSiteHousingLayouts.mysekaiFixtures"}},
		bson.D{{Key: "$match", Value: bson.M{
			"updatedResources.userMysekaiSiteHousingLayouts.mysekaiSiteHousingLayouts.mysekaiFixtures.mysekaiFixtureId": fixtureID,
		}}},
		bson.D{{Key: "$group", Value: bson.M{
			"_id":            "$_id",
			"mysekaiSiteIds": bson.M{"$addToSet": "$updatedResources.userMysekaiSiteHousingLayouts.mysekaiSiteId"},
		}}},
	}

	cursor, err := m.mysekaiCollection.Aggregate(ctx, pipeline)
	if err != nil {
		harukiLogger.Errorf("Failed to aggregate mysekai fixtures: %v", err)
		return nil, err
	}
	defer func(cursor *mongo.Cursor, ctx context.Context) {
		_ = cursor.Close(ctx)
	}(cursor, ctx)

	var results []bson.M
	if err := cursor.All(ctx, &results); err != nil {
		harukiLogger.Errorf("Failed to decode aggregation results: %v", err)
		return nil, err
	}
	return results, nil
}

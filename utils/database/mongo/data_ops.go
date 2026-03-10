package manager

import (
	"context"
	"errors"
	"haruki-suite/utils"
	harukiLogger "haruki-suite/utils/logger"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (m *MongoDBManager) UpdateData(
	ctx context.Context,
	server string,
	userID int64,
	data map[string]any,
	dataType utils.UploadDataType,
) (*mongo.UpdateResult, error) {
	collection := m.getCollectionByDataType(dataType)
	var updateDoc bson.M

	switch dataType {
	case utils.UploadDataTypeSuite:
		oldData, err := m.fetchOldData(ctx, collection, userID)
		if err != nil {
			return nil, err
		}
		finalData := m.buildFinalData(oldData, data)
		finalData[fieldServer] = server
		updateDoc = bson.M{"$set": finalData}
	case utils.UploadDataTypeMysekai:
		data[fieldServer] = server
		updateDoc = bson.M{"$set": data}
	default:
		updatedResources, _ := data["updatedResources"].(map[string]any)
		updateDoc = bson.M{"$set": bson.M{
			fieldServer:                     server,
			fieldUploadTime:                 data[fieldUploadTime],
			fieldUpdatedResourcesHarvestMap: updatedResources["userMysekaiHarvestMaps"],
		}}
	}

	res, err := collection.UpdateOne(
		ctx,
		bson.M{fieldID: userID},
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

func (m *MongoDBManager) fetchOldData(
	ctx context.Context,
	collection *mongo.Collection,
	userID int64,
) (map[string]any, error) {
	projection := bson.M{
		fieldUserEvents:      1,
		fieldUserWorldBlooms: 1,
		fieldID:              0,
	}

	var oldData map[string]any
	err := collection.FindOne(
		ctx,
		bson.M{fieldID: userID},
		options.FindOne().SetProjection(projection),
	).Decode(&oldData)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return make(map[string]any), nil
	}
	if err != nil {
		harukiLogger.Errorf("Failed to fetch old data for user %d: %v", userID, err)
		return nil, err
	}
	return oldData, nil
}

func (m *MongoDBManager) buildFinalData(oldData, data map[string]any) bson.M {
	finalData := bson.M{}

	if mergedEvents := mergeUserEvents(oldData, data); mergedEvents != nil {
		finalData[fieldUserEvents] = mergedEvents
	}
	if mergedBlooms := mergeWorldBlooms(oldData, data); mergedBlooms != nil {
		finalData[fieldUserWorldBlooms] = mergedBlooms
	}

	for key, value := range data {
		if key != fieldUserEvents && key != fieldUserWorldBlooms {
			finalData[key] = value
		}
	}

	return finalData
}

func mergeUserEvents(oldData, newData map[string]any) []any {
	oldEvents, _ := oldData[fieldUserEvents].(bson.A)
	newEvents, _ := newData[fieldUserEvents].([]any)
	allEvents := append(oldEvents, newEvents...)

	latestEvents := make(map[int64]map[string]any)
	for _, ev := range allEvents {
		e, ok := ev.(map[string]any)
		if !ok {
			continue
		}
		eventID := getInt(e, fieldEventID)
		if old, exists := latestEvents[eventID]; !exists || shouldReplaceEvent(e, old) {
			latestEvents[eventID] = e
		}
	}

	if len(latestEvents) == 0 {
		return nil
	}

	arr := make([]any, 0, len(latestEvents))
	for _, v := range latestEvents {
		arr = append(arr, v)
	}
	return arr
}

func shouldReplaceEvent(newEvent, oldEvent map[string]any) bool {
	newPoint := getInt(newEvent, fieldEventPoint)
	oldPoint := getInt(oldEvent, fieldEventPoint)
	return newPoint >= oldPoint
}

type bloomKey struct {
	EventID, CharID int64
}

func mergeWorldBlooms(oldData, newData map[string]any) []any {
	oldBlooms, _ := oldData[fieldUserWorldBlooms].(bson.A)
	newBlooms, _ := newData[fieldUserWorldBlooms].([]any)
	allBlooms := append(oldBlooms, newBlooms...)

	latestBlooms := make(map[bloomKey]map[string]any)
	for _, bv := range allBlooms {
		b, ok := bv.(map[string]any)
		if !ok {
			continue
		}
		key := bloomKey{
			EventID: getInt(b, fieldEventID),
			CharID:  getInt(b, fieldGameCharacterID),
		}
		if old, exists := latestBlooms[key]; !exists || shouldReplaceBloom(b, old) {
			latestBlooms[key] = b
		}
	}

	if len(latestBlooms) == 0 {
		return nil
	}

	arr := make([]any, 0, len(latestBlooms))
	for _, v := range latestBlooms {
		arr = append(arr, v)
	}
	return arr
}

func shouldReplaceBloom(newBloom, oldBloom map[string]any) bool {
	newPoint := getInt(newBloom, fieldWorldBloomChapterPoint)
	oldPoint := getInt(oldBloom, fieldWorldBloomChapterPoint)
	return newPoint >= oldPoint
}

func (m *MongoDBManager) GetData(
	ctx context.Context,
	userID int64,
	server string,
	dataType utils.UploadDataType,
) (bson.M, error) {
	collection := m.getCollectionByDataType(dataType)
	var result bson.M

	err := collection.FindOne(
		ctx,
		bson.M{fieldID: userID, fieldServer: server},
	).Decode(&result)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		harukiLogger.Errorf("Failed to get data for user %d: %v", userID, err)
	}
	return result, err
}

func (m *MongoDBManager) GetDataWithProjection(
	ctx context.Context,
	userID int64,
	server string,
	dataType utils.UploadDataType,
	projection bson.M,
) (bson.D, error) {
	collection := m.getCollectionByDataType(dataType)
	filter := bson.M{fieldID: userID, fieldServer: server}

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

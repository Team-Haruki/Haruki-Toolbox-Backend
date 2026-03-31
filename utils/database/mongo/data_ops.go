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
	if mergedGachas := mergeUserGachas(oldData, data); mergedGachas != nil {
		finalData[fieldUserGachas] = mergedGachas
	}

	for key, value := range data {
		if key != fieldUserEvents && key != fieldUserWorldBlooms && key != fieldUserGachas {
			finalData[key] = value
		}
	}

	return finalData
}

func mergeUserEvents(oldData, newData map[string]any) []any {
	oldEvents := extractAnySlice(oldData[fieldUserEvents])
	newEvents := extractAnySlice(newData[fieldUserEvents])
	allEvents := append(oldEvents, newEvents...)

	latestEvents := make(map[int64]map[string]any)
	for _, ev := range allEvents {
		e, ok := normalizeDocument(ev)
		if !ok {
			continue
		}
		eventID, ok := getRequiredInt(e, fieldEventID)
		if !ok {
			continue
		}
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

	// Higher eventPoint always wins
	if newPoint > oldPoint {
		return true
	}
	// Lower eventPoint never wins
	if newPoint < oldPoint {
		return false
	}
	// Equal eventPoint: prefer the one with rank field (post-event data is more complete)
	_, newHasRank := newEvent[fieldEventRank]
	_, oldHasRank := oldEvent[fieldEventRank]
	if newHasRank && !oldHasRank {
		return true
	}
	// If both have rank or neither has rank, keep existing (don't replace)
	return false
}

type bloomKey struct {
	EventID, CharID int64
}

func mergeWorldBlooms(oldData, newData map[string]any) []any {
	oldBlooms := extractAnySlice(oldData[fieldUserWorldBlooms])
	newBlooms := extractAnySlice(newData[fieldUserWorldBlooms])
	allBlooms := append(oldBlooms, newBlooms...)

	latestBlooms := make(map[bloomKey]map[string]any)
	for _, bv := range allBlooms {
		b, ok := normalizeDocument(bv)
		if !ok {
			continue
		}
		eventID, ok := getRequiredInt(b, fieldEventID)
		if !ok {
			continue
		}
		charID, ok := getRequiredInt(b, fieldGameCharacterID)
		if !ok {
			continue
		}
		key := bloomKey{
			EventID: eventID,
			CharID:  charID,
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

type gachaKey struct {
	GachaID         int64
	GachaBehaviorID int64
}

func mergeUserGachas(oldData, newData map[string]any) []any {
	oldGachas := extractAnySlice(oldData[fieldUserGachas])
	newGachas := extractAnySlice(newData[fieldUserGachas])
	allGachas := append(oldGachas, newGachas...)

	latestGachas := make(map[gachaKey]map[string]any)
	for _, gv := range allGachas {
		gacha, ok := normalizeDocument(gv)
		if !ok {
			continue
		}
		gachaID, ok := getRequiredInt(gacha, fieldGachaID)
		if !ok {
			continue
		}
		gachaBehaviorID, ok := getRequiredInt(gacha, fieldGachaBehaviorID)
		if !ok {
			continue
		}
		key := gachaKey{
			GachaID:         gachaID,
			GachaBehaviorID: gachaBehaviorID,
		}
		if old, exists := latestGachas[key]; !exists || shouldReplaceGacha(gacha, old) {
			latestGachas[key] = gacha
		}
	}

	if len(latestGachas) == 0 {
		return nil
	}

	arr := make([]any, 0, len(latestGachas))
	for _, v := range latestGachas {
		arr = append(arr, v)
	}
	return arr
}

func shouldReplaceGacha(newGacha, oldGacha map[string]any) bool {
	newLastSpinAt := getInt(newGacha, fieldLastSpinAt)
	oldLastSpinAt := getInt(oldGacha, fieldLastSpinAt)
	return newLastSpinAt >= oldLastSpinAt
}

func extractAnySlice(value any) []any {
	switch typed := value.(type) {
	case nil:
		return nil
	case []any:
		return typed
	case bson.A:
		return []any(typed)
	default:
		return nil
	}
}

func normalizeDocument(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case bson.M:
		return map[string]any(typed), true
	case bson.D:
		converted := make(map[string]any, len(typed))
		for _, item := range typed {
			converted[item.Key] = item.Value
		}
		return converted, true
	default:
		return nil, false
	}
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

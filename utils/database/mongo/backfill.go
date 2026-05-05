package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"haruki-suite/utils"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type SuiteBackfillOptions struct {
	Apply        bool
	FilterServer bool
}

type SuiteBackfillResult struct {
	UserID int64

	Matched  bool
	Applied  bool
	Modified bool

	EventsBefore      int
	EventsAfter       int
	WorldBloomsBefore int
	WorldBloomsAfter  int
	GachasBefore      int
	GachasAfter       int
}

func (r SuiteBackfillResult) EventsDelta() int {
	return r.EventsAfter - r.EventsBefore
}

func (r SuiteBackfillResult) WorldBloomsDelta() int {
	return r.WorldBloomsAfter - r.WorldBloomsBefore
}

func (r SuiteBackfillResult) GachasDelta() int {
	return r.GachasAfter - r.GachasBefore
}

func (m *MongoDBManager) BackfillSuiteMergeFields(
	ctx context.Context,
	server string,
	userID int64,
	data map[string]any,
	opts SuiteBackfillOptions,
) (SuiteBackfillResult, error) {
	result := SuiteBackfillResult{
		UserID:  userID,
		Applied: opts.Apply,
	}

	collection := m.getCollectionByDataType(utils.UploadDataTypeSuite)
	oldData, matched, err := fetchSuiteBackfillOldData(ctx, collection, server, userID, opts.FilterServer)
	if err != nil {
		return result, err
	}
	result.Matched = matched
	if !matched {
		return result, nil
	}

	result.EventsBefore = len(extractAnySlice(oldData[fieldUserEvents]))
	result.WorldBloomsBefore = len(extractAnySlice(oldData[fieldUserWorldBlooms]))
	result.GachasBefore = len(extractAnySlice(oldData[fieldUserGachas]))

	finalData := buildSuiteBackfillData(oldData, data)
	result.EventsAfter = result.EventsBefore
	result.WorldBloomsAfter = result.WorldBloomsBefore
	result.GachasAfter = result.GachasBefore
	if mergedEvents := extractAnySlice(finalData[fieldUserEvents]); mergedEvents != nil {
		result.EventsAfter = len(mergedEvents)
	}
	if mergedBlooms := extractAnySlice(finalData[fieldUserWorldBlooms]); mergedBlooms != nil {
		result.WorldBloomsAfter = len(mergedBlooms)
	}
	if mergedGachas := extractAnySlice(finalData[fieldUserGachas]); mergedGachas != nil {
		result.GachasAfter = len(mergedGachas)
	}

	if len(finalData) == 0 || !opts.Apply {
		return result, nil
	}

	filter := bson.M{fieldID: userID}
	if opts.FilterServer {
		filter[fieldServer] = server
	}
	updateResult, err := collection.UpdateOne(ctx, filter, bson.M{"$set": finalData})
	if err != nil {
		return result, err
	}
	result.Modified = updateResult.ModifiedCount > 0
	return result, nil
}

func fetchSuiteBackfillOldData(
	ctx context.Context,
	collection *mongo.Collection,
	server string,
	userID int64,
	filterServer bool,
) (map[string]any, bool, error) {
	projection := bson.M{
		fieldUserEvents:      1,
		fieldUserWorldBlooms: 1,
		fieldUserGachas:      1,
		fieldID:              0,
	}
	filter := bson.M{fieldID: userID}
	if filterServer {
		filter[fieldServer] = server
	}

	var oldData map[string]any
	err := collection.FindOne(
		ctx,
		filter,
		options.FindOne().SetProjection(projection),
	).Decode(&oldData)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return make(map[string]any), false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return oldData, true, nil
}

func buildSuiteBackfillData(oldData, data map[string]any) bson.M {
	finalData := bson.M{}

	if len(extractAnySlice(data[fieldUserEvents])) > 0 {
		if mergedEvents := mergeUserEvents(oldData, data); mergedEvents != nil {
			if suiteFieldChanged(extractAnySlice(oldData[fieldUserEvents]), mergedEvents, eventBackfillKey) {
				finalData[fieldUserEvents] = mergedEvents
			}
		}
	}
	if len(extractAnySlice(data[fieldUserWorldBlooms])) > 0 {
		if mergedBlooms := mergeWorldBlooms(oldData, data); mergedBlooms != nil {
			if suiteFieldChanged(extractAnySlice(oldData[fieldUserWorldBlooms]), mergedBlooms, worldBloomBackfillKey) {
				finalData[fieldUserWorldBlooms] = mergedBlooms
			}
		}
	}
	if len(extractAnySlice(data[fieldUserGachas])) > 0 {
		if mergedGachas := mergeUserGachas(oldData, data); mergedGachas != nil {
			if suiteFieldChanged(extractAnySlice(oldData[fieldUserGachas]), mergedGachas, gachaBackfillKey) {
				finalData[fieldUserGachas] = mergedGachas
			}
		}
	}

	return finalData
}

func suiteFieldChanged(
	oldItems []any,
	mergedItems []any,
	keyFunc func(map[string]any) (string, bool),
) bool {
	oldByKey := make(map[string]map[string]any, len(oldItems))
	for _, item := range oldItems {
		doc, ok := normalizeDocument(item)
		if !ok {
			continue
		}
		key, ok := keyFunc(doc)
		if !ok {
			continue
		}
		oldByKey[key] = doc
	}

	for _, item := range mergedItems {
		doc, ok := normalizeDocument(item)
		if !ok {
			continue
		}
		key, ok := keyFunc(doc)
		if !ok {
			continue
		}
		oldDoc, exists := oldByKey[key]
		if !exists || !documentsEqual(oldDoc, doc) {
			return true
		}
	}
	return false
}

func eventBackfillKey(doc map[string]any) (string, bool) {
	eventID, ok := getRequiredInt(doc, fieldEventID)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%d", eventID), true
}

func worldBloomBackfillKey(doc map[string]any) (string, bool) {
	eventID, ok := getRequiredInt(doc, fieldEventID)
	if !ok {
		return "", false
	}
	charID, ok := getRequiredInt(doc, fieldGameCharacterID)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%d:%d", eventID, charID), true
}

func gachaBackfillKey(doc map[string]any) (string, bool) {
	gachaID, ok := getRequiredInt(doc, fieldGachaID)
	if !ok {
		return "", false
	}
	gachaBehaviorID, ok := getRequiredInt(doc, fieldGachaBehaviorID)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%d:%d", gachaID, gachaBehaviorID), true
}

func documentsEqual(a, b map[string]any) bool {
	aBytes, aErr := json.Marshal(a)
	bBytes, bErr := json.Marshal(b)
	if aErr != nil || bErr != nil {
		return false
	}
	return bytes.Equal(aBytes, bBytes)
}

package manager

import (
	"context"
	"errors"
	"fmt"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/gamemerge"
	"strings"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// validateNoMongoOperatorKeys rejects attacker-controlled field names that Mongo
// would interpret as a dotted field path or an operator-bearing key. Upload payloads
// are decrypted with the public game client key, so their keys are untrusted.
func validateNoMongoOperatorKeys(value any) error {
	switch v := value.(type) {
	case map[string]any:
		for k, val := range v {
			if strings.ContainsAny(k, ".$") {
				return fmt.Errorf("invalid field name %q", k)
			}
			if err := validateNoMongoOperatorKeys(val); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range v {
			if err := validateNoMongoOperatorKeys(item); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *MongoDBManager) UpdateData(
	ctx context.Context,
	server string,
	userID int64,
	data map[string]any,
	dataType utils.UploadDataType,
) (*mongo.UpdateResult, error) {
	if err := validateNoMongoOperatorKeys(data); err != nil {
		harukiLogger.Warnf("Rejected upload with invalid field name for user %d: %v", userID, err)
		return nil, err
	}
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
		fieldUserGachas:      1,
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

// The three history merges live in utils/database/gamedata/gamemerge so the
// MongoDB path and the PostgreSQL path run the SAME implementation. Keeping one
// copy is what makes the existing merge tests evidence that the cutover did not
// change behaviour.

// bsonNormalizer teaches the shared merges the shapes the MongoDB driver hands
// back, on top of the plain Go ones.
type bsonNormalizer struct{ gamemerge.JSONNormalizer }

func (bsonNormalizer) Slice(value any) []any {
	if a, ok := value.(bson.A); ok {
		return []any(a)
	}
	return gamemerge.AnySlice(value)
}

func (bsonNormalizer) Document(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case bson.M:
		return map[string]any(typed), true
	case bson.D:
		converted := make(map[string]any, len(typed))
		for _, item := range typed {
			converted[item.Key] = item.Value
		}
		return converted, true
	}
	return gamemerge.Document(value)
}

func mergeUserEvents(oldData, newData map[string]any) []any {
	return gamemerge.Events(bsonNormalizer{}, oldData[fieldUserEvents], newData[fieldUserEvents])
}

func mergeWorldBlooms(oldData, newData map[string]any) []any {
	return gamemerge.WorldBlooms(bsonNormalizer{}, oldData[fieldUserWorldBlooms], newData[fieldUserWorldBlooms])
}

func mergeUserGachas(oldData, newData map[string]any) []any {
	return gamemerge.Gachas(bsonNormalizer{}, oldData[fieldUserGachas], newData[fieldUserGachas])
}

func extractAnySlice(value any) []any { return bsonNormalizer{}.Slice(value) }

func normalizeDocument(value any) (map[string]any, bool) {
	return bsonNormalizer{}.Document(value)
}

func (m *MongoDBManager) GetData(
	ctx context.Context,
	userID int64,
	server string,
	dataType utils.UploadDataType,
) (bson.D, error) {
	collection := m.getCollectionByDataType(dataType)
	var result bson.D

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

// GetUploadTime reads only the stored document's upload_time stamp, for the
// conditional (?known_upload_time) read path. The projection keeps the implicit
// _id inclusion so an existing document always yields a non-empty result even
// when it predates the upload_time stamp; found therefore means "document
// exists", and uploadTime is 0 when the stamp is missing or non-numeric.
func (m *MongoDBManager) GetUploadTime(
	ctx context.Context,
	userID int64,
	server string,
	dataType utils.UploadDataType,
) (uploadTime int64, found bool, err error) {
	result, err := m.GetDataWithProjection(ctx, userID, server, dataType, bson.M{fieldUploadTime: 1})
	if err != nil {
		return 0, false, err
	}
	if len(result) == 0 {
		return 0, false, nil
	}
	return uploadTimeFromResult(result), true, nil
}

func uploadTimeFromResult(result bson.D) int64 {
	for _, elem := range result {
		if elem.Key == fieldUploadTime {
			parsed, _ := toInt64(elem.Value)
			return parsed
		}
	}
	return 0
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

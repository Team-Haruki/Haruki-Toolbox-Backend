package manager

import (
	"context"
	harukiLogger "haruki-suite/utils/logger"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func (m *MongoDBManager) SearchPutMysekaiFixtureUser(
	ctx context.Context,
	server string,
	fixtureID int,
) ([]bson.M, error) {
	pipeline := mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.M{fieldServer: server}}},
		bson.D{{Key: "$unwind", Value: "$updatedResources.userMysekaiSiteHousingLayouts"}},
		bson.D{{Key: "$unwind", Value: "$updatedResources.userMysekaiSiteHousingLayouts.mysekaiSiteHousingLayouts"}},
		bson.D{{Key: "$unwind", Value: "$updatedResources.userMysekaiSiteHousingLayouts.mysekaiSiteHousingLayouts.mysekaiFixtures"}},
		bson.D{{Key: "$match", Value: bson.M{
			"updatedResources.userMysekaiSiteHousingLayouts.mysekaiSiteHousingLayouts.mysekaiFixtures.mysekaiFixtureId": fixtureID,
		}}},
		bson.D{{Key: "$group", Value: bson.M{
			fieldID:          "$_id",
			"mysekaiSiteIds": bson.M{"$addToSet": "$updatedResources.userMysekaiSiteHousingLayouts.mysekaiSiteId"},
		}}},
	}

	cursor, err := m.mysekaiCollection.Aggregate(ctx, pipeline)
	if err != nil {
		harukiLogger.Errorf("Failed to aggregate mysekai fixtures: %v", err)
		return nil, err
	}
	defer closeCursor(ctx, cursor)

	return decodeBsonMResults(ctx, cursor, "aggregation results")
}

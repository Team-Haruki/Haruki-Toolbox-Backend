package data

import (
	"context"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	"github.com/gofiber/fiber/v3"
	"go.mongodb.org/mongo-driver/v2/bson"
)

var DeckRecommendSuiteKeys = []string{
	"userGamedata",
	"userAreas",
	"userCards",
	"userCharacters",
	"userHonors",
	"userDecks",
	"userChallengeLiveSoloDecks",
}

var DeckRecommendMysekaiKeys = []string{
	"userMysekaiCanvases",
	"userMysekaiFixtureGameCharacterPerformanceBonuses",
	"userMysekaiGates",
}

func LoadDeckRecommendUserData(
	ctx context.Context,
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	userID int64,
	server harukiUtils.SupportedDataUploadServer,
	includeMysekai bool,
) (map[string]any, error) {
	suiteData, err := LoadDeckRecommendSuiteData(ctx, apiHelper, userID, server)
	if err != nil {
		return nil, err
	}
	if !includeMysekai {
		return suiteData, nil
	}

	mysekaiData, err := LoadDeckRecommendMysekaiData(ctx, apiHelper, userID, server)
	if err != nil {
		return nil, err
	}
	for key, value := range mysekaiData {
		suiteData[key] = value
	}
	return suiteData, nil
}

func LoadDeckRecommendSuiteData(
	ctx context.Context,
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	userID int64,
	server harukiUtils.SupportedDataUploadServer,
) (map[string]any, error) {
	if apiHelper == nil || apiHelper.DBManager == nil || apiHelper.DBManager.Mongo == nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "mongo data source is not configured")
	}

	result, err := apiHelper.DBManager.Mongo.GetDataWithProjection(
		ctx,
		userID,
		string(server),
		harukiUtils.UploadDataTypeSuite,
		buildSuiteProjection(DeckRecommendSuiteKeys),
	)
	if err != nil {
		harukiLogger.Errorf("Failed to fetch deck recommend suite data: %v", err)
		return nil, fiber.NewError(fiber.StatusInternalServerError, "failed to get suite data")
	}
	if len(result) == 0 {
		return nil, fiber.NewError(fiber.StatusNotFound, "suite data not found")
	}

	return BSONDToMap(buildSuiteResponse(result, DeckRecommendSuiteKeys)), nil
}

func LoadDeckRecommendMysekaiData(
	ctx context.Context,
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	userID int64,
	server harukiUtils.SupportedDataUploadServer,
) (map[string]any, error) {
	if apiHelper == nil || apiHelper.DBManager == nil || apiHelper.DBManager.Mongo == nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "mongo data source is not configured")
	}

	result, err := apiHelper.DBManager.Mongo.GetDataWithProjection(
		ctx,
		userID,
		string(server),
		harukiUtils.UploadDataTypeMysekai,
		buildMysekaiProjection(DeckRecommendMysekaiKeys),
	)
	if err != nil {
		harukiLogger.Errorf("Failed to fetch deck recommend mysekai data: %v", err)
		return nil, fiber.NewError(fiber.StatusInternalServerError, "failed to get mysekai data")
	}
	if len(result) == 0 {
		return nil, fiber.NewError(fiber.StatusNotFound, "mysekai data not found")
	}

	return BSONDToMap(filterBSOND(result, DeckRecommendMysekaiKeys)), nil
}

func filterBSOND(result bson.D, keys []string) bson.D {
	keySet := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		keySet[key] = struct{}{}
	}
	filtered := make(bson.D, 0, len(keys))
	for _, elem := range result {
		if _, ok := keySet[elem.Key]; ok {
			filtered = append(filtered, elem)
		}
	}
	return filtered
}

package data

import (
	"bytes"
	"context"
	json "encoding/json/v2"

	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/jsonvalue"

	"github.com/gofiber/fiber/v3"
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
	return loadDeckRecommendData(ctx, apiHelper, userID, server, true)
}

func LoadDeckRecommendMysekaiData(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID int64, server harukiUtils.SupportedDataUploadServer) (map[string]any, error) {
	return loadDeckRecommendData(ctx, apiHelper, userID, server, false)
}

func loadDeckRecommendData(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID int64, server harukiUtils.SupportedDataUploadServer, suite bool) (map[string]any, error) {
	if apiHelper == nil || apiHelper.DBManager == nil || apiHelper.DBManager.GameData == nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "game data source is not configured")
	}
	gd := apiHelper.DBManager.GameData
	var body []byte
	var err error
	if suite {
		body, err = suiteBodyFromPostgres(ctx, gd.Suite(), userID, server, DeckRecommendSuiteKeys, false)
	} else {
		body, err = mysekaiBodyFromPostgres(ctx, gd.Mysekai(), userID, server, DeckRecommendMysekaiKeys)
	}
	if err != nil {
		return nil, err
	}
	var result map[string]any

	if err := json.UnmarshalRead(bytes.NewReader(body), &result, jsonvalue.Numbers); err != nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "failed to decode game data")
	}
	return result, nil
}

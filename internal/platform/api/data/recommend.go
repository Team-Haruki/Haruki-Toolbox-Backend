package data

import (
	"bytes"
	"context"
	json "encoding/json/v2"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/codec/jsonvalue"

	"github.com/gofiber/fiber/v3"
)

// DeckRecommendSuiteKeys is everything the deck engine reads from a player, in
// every mode. The mysekai deck mode's inputs are suite tables too: the canvas,
// fixture character and gate bonuses below are top-level suite keys, so the
// mysekai upload is never read.
var DeckRecommendSuiteKeys = []string{
	"userGamedata",
	"userAreas",
	"userCards",
	"userCharacters",
	"userHonors",
	"userDecks",
	"userChallengeLiveSoloDecks",
	"userMysekaiCanvases",
	"userMysekaiFixtureGameCharacterPerformanceBonuses",
	"userMysekaiGates",
}

// LoadDeckRecommendUserData reads the deck-recommend subset of a player's
// suite upload. Both recommend-data modes use it unchanged.
func LoadDeckRecommendUserData(
	ctx context.Context,
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	userID int64,
	server harukiUtils.SupportedDataUploadServer,
) (map[string]any, error) {
	if apiHelper == nil || apiHelper.DBManager == nil || apiHelper.DBManager.GameData == nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "game data source is not configured")
	}
	body, err := suiteBodyFromPostgres(ctx, apiHelper.DBManager.GameData.Suite(), userID, server, DeckRecommendSuiteKeys, false)
	if err != nil {
		return nil, err
	}
	var result map[string]any

	if err := json.UnmarshalRead(bytes.NewReader(body), &result, jsonvalue.Numbers); err != nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "failed to decode game data")
	}
	return result, nil
}

package data

import (
	"errors"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata"
)

func requireFiberStatus(t *testing.T, err error, status int, message string) {
	t.Helper()
	var fiberErr *fiber.Error
	if !errors.As(err, &fiberErr) {
		t.Fatalf("expected *fiber.Error, got %T %v", err, err)
	}
	if fiberErr.Code != status || fiberErr.Message != message {
		t.Fatalf("got %d %q, want %d %q", fiberErr.Code, fiberErr.Message, status, message)
	}
}

func TestDeckRecommendRequiresGameDataSource(t *testing.T) {
	const msg = "game data source is not configured"
	for name, helper := range map[string]*api.HarukiToolboxRouterHelpers{
		"nil helper":     nil,
		"nil db manager": {},
		"nil game data":  {DBManager: &database.HarukiToolboxDBManager{}},
	} {
		t.Run(name, func(t *testing.T) {
			for _, includeMysekai := range []bool{false, true} {
				got, err := LoadDeckRecommendUserData(t.Context(), helper, 1, harukiUtils.SupportedDataUploadServerJP, includeMysekai)
				if got != nil {
					t.Fatalf("unexpected data %v", got)
				}
				requireFiberStatus(t, err, fiber.StatusInternalServerError, msg)
			}
			got, err := LoadDeckRecommendMysekaiData(t.Context(), helper, 1, harukiUtils.SupportedDataUploadServerJP)
			if got != nil {
				t.Fatalf("unexpected data %v", got)
			}
			requireFiberStatus(t, err, fiber.StatusInternalServerError, msg)
		})
	}
}

// An unknown region can never have a row; both stores must report it as
// player-not-found without touching the database.
func TestDeckRecommendUnknownRegionIsNotFound(t *testing.T) {
	helper := &api.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{GameData: gamedata.NewService(&gamedata.Pool{})},
	}
	const server = harukiUtils.SupportedDataUploadServer("xx")
	got, err := LoadDeckRecommendUserData(t.Context(), helper, 1, server, true)
	if got != nil {
		t.Fatalf("unexpected data %v", got)
	}
	requireFiberStatus(t, err, fiber.StatusNotFound, "Player data not found.")

	got, err = LoadDeckRecommendMysekaiData(t.Context(), helper, 1, server)
	if got != nil {
		t.Fatalf("unexpected data %v", got)
	}
	requireFiberStatus(t, err, fiber.StatusNotFound, "Player data not found.")
}

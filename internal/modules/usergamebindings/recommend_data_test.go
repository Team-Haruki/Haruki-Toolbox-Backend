package usergamebindings

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"

	"github.com/gofiber/fiber/v3"
)

func TestParseDeckRecommendDataMode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		raw       string
		want      deckRecommendDataMode
		wantError bool
	}{
		{name: "empty defaults to suite", raw: "", want: deckRecommendDataModeSuite},
		{name: "suite", raw: "suite", want: deckRecommendDataModeSuite},
		{name: "mysekai trims and lowercases", raw: " MySekai ", want: deckRecommendDataModeMysekai},
		{name: "invalid", raw: "all", wantError: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseDeckRecommendDataMode(tc.raw)
			if tc.wantError {
				if err == nil || err.Code != fiber.StatusBadRequest {
					t.Fatalf("expected bad request error, got %#v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("mode = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateDeckRecommendDataBinding(t *testing.T) {
	t.Parallel()

	ownedVerified := &postgresql.GameAccountBinding{Verified: true}
	ownedVerified.Edges.User = &postgresql.User{ID: "u-1"}
	if err := validateVerifiedOwnedGameAccountBinding(ownedVerified, "u-1"); err != nil {
		t.Fatalf("owned verified binding should be accepted: %v", err)
	}

	if err := validateVerifiedOwnedGameAccountBinding(nil, "u-1"); err == nil || err.Code != fiber.StatusNotFound {
		t.Fatalf("nil binding should map to 404, got %#v", err)
	}

	orphan := &postgresql.GameAccountBinding{Verified: true}
	if err := validateVerifiedOwnedGameAccountBinding(orphan, "u-1"); err == nil || err.Code != fiber.StatusConflict {
		t.Fatalf("orphan binding should map to 409, got %#v", err)
	}

	ownedByOther := &postgresql.GameAccountBinding{Verified: true}
	ownedByOther.Edges.User = &postgresql.User{ID: "u-2"}
	if err := validateVerifiedOwnedGameAccountBinding(ownedByOther, "u-1"); err == nil || err.Code != fiber.StatusForbidden {
		t.Fatalf("other user's binding should map to 403, got %#v", err)
	}

	unverified := &postgresql.GameAccountBinding{Verified: false}
	unverified.Edges.User = &postgresql.User{ID: "u-1"}
	if err := validateVerifiedOwnedGameAccountBinding(unverified, "u-1"); err == nil || err.Code != fiber.StatusBadRequest {
		t.Fatalf("unverified binding should map to 400, got %#v", err)
	}
}

// recommendDataTestApp serves recommend-data for a jp account owned by "owner"
// and shared with "grantee" through a suite-only grant. gd may be nil.
func recommendDataTestApp(t *testing.T, gd *gamedata.Service) *fiber.App {
	t.Helper()
	db := enttest.Open(t, "sqlite3", "file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	for _, id := range []string{"owner", "grantee", "stranger"} {
		if _, err := db.User.Create().SetID(id).SetName(id).SetEmail(id + "@example.com").Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.GameAccountBinding.Create().SetServer("jp").SetGameUserID(recommendTestGameUserID).SetVerified(true).SetUserID("owner").Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GameAccountDataGrant.Create().SetOwnerUserID("owner").SetGranteeUserID("grantee").
		SetServer("jp").SetGameUserID(recommendTestGameUserID).SetDataType("suite").SetExpiresAt(time.Now().Add(time.Hour)).Save(ctx); err != nil {
		t.Fatal(err)
	}
	manager := &database.HarukiToolboxDBManager{DB: db}
	if gd != nil {
		manager.GameData = gd
	}
	helper := &api.HarukiToolboxRouterHelpers{DBManager: manager}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("userID", c.Get("X-Test-User"))
		return c.Next()
	})
	app.Get("/:server/:game_user_id/recommend-data", handleGetDeckRecommendData(helper))
	return app
}

const recommendTestGameUserID = "28808221489823746"

func getRecommendData(t *testing.T, app *fiber.App, user, query string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(fiber.MethodGet, "/jp/"+recommendTestGameUserID+"/recommend-data"+query, nil)
	req.Header.Set("X-Test-User", user)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

// The mysekai mode reads only suite, so a suite grant is enough: the request
// passes the access check (no mysekai grant exists) and reaches the read.
func TestDeckRecommendMysekaiModeNeedsOnlySuiteAccess(t *testing.T) {
	app := recommendDataTestApp(t, nil)
	for _, user := range []string{"owner", "grantee"} {
		for _, query := range []string{"?mode=mysekai", "?data_type=mysekai", "?mode=suite", ""} {
			status, body := getRecommendData(t, app, user, query)
			if status != fiber.StatusInternalServerError || !strings.Contains(string(body), "game data source is not configured") {
				t.Fatalf("%s %q: got %d %s, want the read to be reached", user, query, status, body)
			}
		}
	}
	if status, _ := getRecommendData(t, app, "stranger", "?mode=mysekai"); status != fiber.StatusForbidden {
		t.Fatalf("stranger got %d, want 403", status)
	}
}

// With a real game-data store: an account with a suite upload and no mysekai
// upload answers 200 in mysekai mode, with the gate, canvas and fixture-bonus
// tables taken from suite.
func TestDeckRecommendMysekaiModeReturnsSuiteMysekaiTables(t *testing.T) {
	dsn := os.Getenv("GAMEDATA_RECOMMEND_TEST_PG")
	if dsn == "" {
		t.Skip("set GAMEDATA_RECOMMEND_TEST_PG to a disposable database")
	}
	ctx := t.Context()
	p, err := gamedata.NewPool(ctx, gamedata.PoolConfig{URL: dsn, MaxConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err = gamedata.EnsureSchema(ctx, p, true, catalog.Suite(), catalog.Mysekai()); err != nil {
		t.Fatal(err)
	}
	gd := gamedata.NewService(p)
	id, _ := strconv.ParseInt(recommendTestGameUserID, 10, 64)
	suite := map[string]any{
		"upload_time":         int64(100),
		"userGamedata":        map[string]any{"userId": id},
		"userCards":           []any{map[string]any{"cardId": 1}},
		"userMysekaiGates":    []any{map[string]any{"mysekaiGateId": 1, "mysekaiGateLevel": 30}},
		"userMysekaiCanvases": []any{map[string]any{"mysekaiFixtureId": 2, "cardId": 1}},
		"userMysekaiFixtureGameCharacterPerformanceBonuses": []any{map[string]any{"gameCharacterId": 1, "totalBonusRate": 5}},
	}
	if _, err = gd.Suite().Write(ctx, id, "jp", suite, gamedata.WriteSuite, gd.Limits()); err != nil {
		t.Fatal(err)
	}
	app := recommendDataTestApp(t, gd)
	for _, query := range []string{"?mode=mysekai", "?data_type=mysekai", "?mode=suite"} {
		status, body := getRecommendData(t, app, "grantee", query)
		if status != fiber.StatusOK {
			t.Fatalf("%q: got %d %s, want 200", query, status, body)
		}
		var resp struct {
			Data deckRecommendDataResponse `json:"updatedData"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"userMysekaiGates", "userMysekaiCanvases", "userMysekaiFixtureGameCharacterPerformanceBonuses"} {
			rows, ok := resp.Data.UserData[key].([]any)
			if !ok || len(rows) != 1 {
				t.Fatalf("%q: %s = %#v, want one suite row", query, key, resp.Data.UserData[key])
			}
		}
	}
}

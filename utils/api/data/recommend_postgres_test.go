package data

import (
	"os"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/jsonvalue"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

func TestDeckRecommendationUsesPostgresAndPreservesNumericIDs(t *testing.T) {
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
	id := int64(28808221489823746)
	suite := map[string]any{"upload_time": int64(100), "userGamedata": map[string]any{"userId": id, "name": "fixture", "rank": 99, "secret": "hidden"}, "userCards": []any{map[string]any{"cardId": 1}}, "userDecks": []any{}}
	if _, err = gd.Suite().Write(ctx, id, "jp", suite, gamedata.WriteSuite, gd.Limits()); err != nil {
		t.Fatal(err)
	}
	mysekai := map[string]any{"userMysekaiCanvases": []any{map[string]any{"id": 7}}}
	if _, err = gd.Mysekai().Write(ctx, id, "jp", mysekai, gamedata.WriteMysekai, gd.Limits()); err != nil {
		t.Fatal(err)
	}
	helper := &api.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{GameData: gd}}
	got, err := LoadDeckRecommendUserData(ctx, helper, id, "jp", true)
	if err != nil {
		t.Fatal(err)
	}
	profile := got["userGamedata"].(map[string]any)
	if profile["userId"] != jsonvalue.Number("28808221489823746") {
		t.Fatalf("ID lost precision: %v", profile["userId"])
	}
	if _, ok := profile["secret"]; ok {
		t.Fatal("profile projection leaked an unrequested field")
	}
	if _, ok := got["userMysekaiCanvases"]; !ok {
		t.Fatal("mysekai recommendation data missing")
	}
	stamp, found, err := readUploadTime(ctx, helper, "jp", "suite", id)
	if err != nil || !found || stamp <= 0 {
		t.Fatalf("PG cache stamp unavailable: %d %v %v", stamp, found, err)
	}
}

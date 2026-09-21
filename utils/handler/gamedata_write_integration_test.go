package handler

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata/catalog"
)

// Use a disposable database: this test creates and writes game-data tables.
func TestAuthoritativeUploadPersistsFullDataAndReturnsWriteFailure(t *testing.T) {
	dsn := os.Getenv("GAMEDATA_HANDLER_TEST_PG")
	if dsn == "" {
		t.Skip("set GAMEDATA_HANDLER_TEST_PG to a disposable database")
	}
	ctx := context.Background()
	p, err := gamedata.NewPool(ctx, gamedata.PoolConfig{URL: dsn, MaxConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err = gamedata.EnsureSchema(ctx, p, true, catalog.Suite(), catalog.Mysekai()); err != nil {
		t.Fatal(err)
	}
	service := gamedata.NewService(p)
	h := &DataHandler{DBManager: &database.HarukiToolboxDBManager{GameData: service}}
	id := int64(28808221489823746)
	payload := map[string]any{"userCostume3dShopItems": []any{map[string]any{"id": 1}}, "upload_time": int64(100)}
	if err := h.PersistUploadData(ctx, payload, utils.SupportedDataUploadServerJP, utils.UploadDataTypeSuite, &id); err != nil {
		t.Fatal(err)
	}
	row, err := service.Suite().Fetch(ctx, id, "jp", []string{"userCostume3dShopItems"})
	if err != nil {
		t.Fatal(err)
	}
	raw, found, err := row.RawValue("userCostume3dShopItems")
	if err != nil || !found || !strings.Contains(string(raw), `"id":1`) {
		t.Fatalf("full upload was not preserved: %s, %v", raw, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := h.PersistUploadData(canceled, payload, utils.SupportedDataUploadServerJP, utils.UploadDataTypeSuite, &id); err == nil {
		t.Fatal("canceled upload reported success")
	}
	p.Close()
	for _, typ := range []utils.UploadDataType{utils.UploadDataTypeSuite, utils.UploadDataTypeMysekai, utils.UploadDataTypeMysekaiBirthdayParty} {
		if err := h.PersistUploadData(ctx, payload, utils.SupportedDataUploadServerJP, typ, &id); err == nil {
			t.Fatalf("%s reported success with closed PostgreSQL pool", typ)
		}
	}
}

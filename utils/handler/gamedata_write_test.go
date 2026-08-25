package handler

import (
	"context"
	"testing"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiGameData "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata"
)

func TestGameDataWriteModeCoversEveryUploadType(t *testing.T) {
	cases := []struct {
		dataType   utils.UploadDataType
		wantMode   harukiGameData.WriteMode
		wantColl   string
		wantMapped bool
	}{
		// suite accumulates the three history keys; mysekai does not; a birthday
		// party writes three columns of a mysekai row. Collapsing any two of
		// these loses data, so each maps to its own mode.
		{utils.UploadDataTypeSuite, harukiGameData.WriteSuite, "suite", true},
		{utils.UploadDataTypeMysekai, harukiGameData.WriteMysekai, "mysekai", true},
		{utils.UploadDataTypeMysekaiBirthdayParty, harukiGameData.WriteBirthdayParty, "mysekai", true},
		{utils.UploadDataType("something-new"), 0, "", false},
	}
	for _, c := range cases {
		mode, coll, ok := gameDataWriteMode(c.dataType)
		if ok != c.wantMapped {
			t.Fatalf("%s: mapped = %v, want %v", c.dataType, ok, c.wantMapped)
		}
		if !ok {
			continue
		}
		if mode != c.wantMode {
			t.Fatalf("%s: mode = %v, want %v", c.dataType, mode, c.wantMode)
		}
		if coll != c.wantColl {
			t.Fatalf("%s: collection = %q, want %q", c.dataType, coll, c.wantColl)
		}
	}
}

// WriteMigrate replaces the whole row. An upload is a partial document, so
// reaching that mode from the upload path would erase every key the upload
// happens not to carry.
func TestGameDataWriteModeNeverSelectsMigrate(t *testing.T) {
	for _, dt := range []utils.UploadDataType{
		utils.UploadDataTypeSuite,
		utils.UploadDataTypeMysekai,
		utils.UploadDataTypeMysekaiBirthdayParty,
	} {
		if mode, _, ok := gameDataWriteMode(dt); ok && mode == harukiGameData.WriteMigrate {
			t.Fatalf("%s maps to WriteMigrate, which would replace the whole row", dt)
		}
	}
}

// The mirror must be inert wherever the game-data store is not configured —
// that is the state every deployment is in before the cutover, and a panic here
// would take down uploads on a build that never touches PostgreSQL.
func TestShadowWriteGameDataIsInertWithoutStore(t *testing.T) {
	var nilHandler *DataHandler
	nilHandler.shadowWriteGameData(context.Background(), map[string]any{},
		utils.SupportedDataUploadServerJP, utils.UploadDataTypeSuite, 1)

	noManager := &DataHandler{}
	noManager.shadowWriteGameData(context.Background(), map[string]any{},
		utils.SupportedDataUploadServerJP, utils.UploadDataTypeSuite, 1)
}

func TestShadowWriteGameDataIgnoresUnknownDataType(t *testing.T) {
	h := &DataHandler{}
	// An unmapped type must return before touching the manager rather than
	// guessing a collection.
	h.shadowWriteGameData(context.Background(), map[string]any{},
		utils.SupportedDataUploadServerJP, utils.UploadDataType("profile"), 1)
}

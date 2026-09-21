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

func TestWriteGameDataFailsWithoutStore(t *testing.T) {
	for _, h := range []*DataHandler{nil, {}} {
		if err := h.writeGameData(context.Background(), map[string]any{}, utils.SupportedDataUploadServerJP, utils.UploadDataTypeSuite, 1); err == nil {
			t.Fatal("upload succeeded without a durable store")
		}
	}
}

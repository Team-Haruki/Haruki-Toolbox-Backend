package handler

import (
	"context"
	"fmt"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiGameData "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata"
)

// gameDataWriteTimeout bounds the authoritative PostgreSQL upload.
const gameDataWriteTimeout = 20 * time.Second

// gameDataWriteMode maps an upload's data type onto the write semantics the
// PostgreSQL store implements for it.
//
// The three modes are not interchangeable. suite merges at the top level AND
// accumulates the three history keys; mysekai merges without history; a birthday
// party writes three columns of a mysekai row and nothing else. Collapsing any
// two of them loses data, which is why the store implements them separately
// rather than taking a flag.
func gameDataWriteMode(dataType utils.UploadDataType) (harukiGameData.WriteMode, string, bool) {
	switch dataType {
	case utils.UploadDataTypeSuite:
		return harukiGameData.WriteSuite, "suite", true
	case utils.UploadDataTypeMysekai:
		return harukiGameData.WriteMysekai, "mysekai", true
	case utils.UploadDataTypeMysekaiBirthdayParty:
		return harukiGameData.WriteBirthdayParty, "mysekai", true
	default:
		return 0, "", false
	}
}

// writeGameData commits the upload before success and downstream fanout.
func (h *DataHandler) writeGameData(
	ctx context.Context,
	data map[string]any,
	server utils.SupportedDataUploadServer,
	dataType utils.UploadDataType,
	gameUserID int64,
) error {
	if h == nil || h.DBManager == nil {
		return fmt.Errorf("game data manager is not configured")
	}
	service := h.DBManager.GameData
	if service == nil {
		return fmt.Errorf("game data store is not configured")
	}
	mode, collection, ok := gameDataWriteMode(dataType)
	if !ok {
		return fmt.Errorf("unsupported game data upload type %q", dataType)
	}
	store := service.StoreFor(collection)
	if store == nil {
		return fmt.Errorf("game data store is not configured")
	}

	writeCtx, cancel := context.WithTimeout(ctx, gameDataWriteTimeout)
	defer cancel()

	stats, err := store.Write(writeCtx, gameUserID, string(server), data, mode, service.Limits())
	if err != nil {
		if h.Logger != nil {
			h.Logger.Errorf(
				"game-data write failed (server=%s dataType=%s gameUserId=%d): %v",
				server, dataType, gameUserID, err,
			)
		}
		return fmt.Errorf("persist game data: %w", err)
	}
	if h.Logger == nil {
		return nil
	}
	// Denied keys are a security control, not a size optimisation: they are tiny,
	// so no byte counter would ever reveal whether the drop still works. Log the
	// count so a regression that starts storing them is visible.
	if len(stats.DeniedDropped) > 0 {
		total := 0
		for _, n := range stats.DeniedDropped {
			total += n
		}
		h.Logger.Debugf(
			"game-data write dropped %d denied-key value(s) (server=%s dataType=%s gameUserId=%d)",
			total, server, dataType, gameUserID,
		)
	}
	if len(stats.AliasConflicts) > 0 {
		h.Logger.Debugf(
			"game-data write saw %d alias conflict(s) (server=%s dataType=%s gameUserId=%d)",
			len(stats.AliasConflicts), server, dataType, gameUserID,
		)
	}
	return nil
}

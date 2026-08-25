package handler

import (
	"context"
	"time"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiGameData "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/gamedata"
)

// gameDataShadowWriteTimeout bounds the PostgreSQL write so a slow or wedged
// game-data pool cannot hold an upload open. MongoDB already has the document by
// the time this runs, so giving up here costs a row that the next upload — or a
// migration re-run — restores.
const gameDataShadowWriteTimeout = 20 * time.Second

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

// shadowWriteGameData mirrors an upload into the PostgreSQL game-data store.
//
// MongoDB stays authoritative while game_data.read_source is "mongo": this write
// exists so the two stores stay in step, and so the read cutover has something
// current to switch to. Once reads come from PostgreSQL this write is what makes
// an upload visible at all, which is why it is synchronous — an asynchronous
// mirror would let a client upload and then read its own stale row.
//
// It NEVER fails the upload. The document is already durable in MongoDB by the
// time this runs; turning a game-data outage into a user-visible upload failure
// would trade a recoverable divergence for a hard one. Divergence is instead
// surfaced by `gamedata-migrate verify`, which is the tool that exists to find
// exactly this.
func (h *DataHandler) shadowWriteGameData(
	ctx context.Context,
	data map[string]any,
	server utils.SupportedDataUploadServer,
	dataType utils.UploadDataType,
	gameUserID int64,
) {
	if h == nil || h.DBManager == nil {
		return
	}
	service := h.DBManager.GameData
	if service == nil {
		// No game_data.url configured: the whole subsystem is off and MongoDB is
		// the only store. Nothing to mirror to.
		return
	}
	mode, collection, ok := gameDataWriteMode(dataType)
	if !ok {
		return
	}
	store := service.StoreFor(collection)
	if store == nil {
		return
	}

	// Detached from the request deadline but still bounded: the caller's context
	// may be seconds from expiry after a large decode, and a mirror that is
	// abandoned halfway is worse than one given its own budget.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), gameDataShadowWriteTimeout)
	defer cancel()

	stats, err := store.Write(writeCtx, gameUserID, string(server), data, mode, service.Limits())
	if err != nil {
		if h.Logger != nil {
			h.Logger.Errorf(
				"game-data shadow write failed (server=%s dataType=%s gameUserId=%d): %v",
				server, dataType, gameUserID, err,
			)
		}
		return
	}
	if h.Logger == nil {
		return
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
			"game-data shadow write dropped %d denied-key value(s) (server=%s dataType=%s gameUserId=%d)",
			total, server, dataType, gameUserID,
		)
	}
	if len(stats.AliasConflicts) > 0 {
		h.Logger.Debugf(
			"game-data shadow write saw %d alias conflict(s) (server=%s dataType=%s gameUserId=%d)",
			len(stats.AliasConflicts), server, dataType, gameUserID,
		)
	}
}

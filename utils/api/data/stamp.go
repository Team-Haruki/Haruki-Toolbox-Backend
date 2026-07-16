package data

import (
	"context"
	"fmt"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"strconv"
)

// ResolveGameDataStamp returns the stored document's current upload_time (0
// when the document is missing or predates the stamp), preferring a short
// Redis memo over a Mongo projection read so the steady-state read path stays
// Redis-only. The memo is the freshness ceiling of the whole cache layer: it
// lives for GameDataStampMemoTTL, is deleted by every per-user cache clear
// (upload, binding changes) and by the allowlist namespace wipe, and a stale
// memo can at worst serve the previous consistent generation for one memo
// lifetime.
func ResolveGameDataStamp(
	ctx context.Context,
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	server harukiUtils.SupportedDataUploadServer,
	dataType harukiUtils.UploadDataType,
	userID int64,
) (int64, error) {
	if apiHelper == nil || apiHelper.DBManager == nil {
		return 0, fmt.Errorf("db manager is nil")
	}
	memoKey := harukiRedis.BuildGameDataStampMemoKey(string(server), string(dataType), userID)
	if apiHelper.DBManager.Redis != nil {
		if raw, found, err := apiHelper.DBManager.Redis.GetRawCache(ctx, memoKey); err == nil && found {
			if stamp, pErr := strconv.ParseInt(raw, 10, 64); pErr == nil {
				return stamp, nil
			}
			// A corrupt memo falls through to Mongo and is overwritten below.
		}
	}
	if apiHelper.DBManager.Mongo == nil {
		return 0, fmt.Errorf("mongo manager is nil")
	}
	stamp, found, err := apiHelper.DBManager.Mongo.GetUploadTime(ctx, userID, string(server), dataType)
	if err != nil {
		return 0, err
	}
	if !found {
		stamp = 0
	}
	if apiHelper.DBManager.Redis != nil {
		// Best-effort: a failed memo write only means the next read resolves
		// from Mongo again.
		if mErr := apiHelper.DBManager.Redis.SetRawCache(ctx, memoKey, strconv.FormatInt(stamp, 10), harukiRedis.GameDataStampMemoTTL); mErr != nil {
			harukiLogger.Warnf("Failed to write game data stamp memo %s: %v", memoKey, mErr)
		}
	}
	return stamp, nil
}

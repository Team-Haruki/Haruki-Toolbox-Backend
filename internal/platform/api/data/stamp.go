package data

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiRedis "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/redis"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
)

// ResolveGameDataStamp returns the stored document's current upload_time (0
// when the document is missing or predates the stamp), preferring a short
// Redis memo over a game-data PostgreSQL read so the steady-state read path stays
// Redis-only. The memo is the freshness ceiling of every stamp-changing write
// path: it lives for GameDataStampMemoTTL and is deleted by every per-user
// cache clear (upload, backfill has its own stamp bump, binding changes) and
// by the allowlist namespace wipe.
//
// confirmed reports whether the stamp came from the memo or a live database
// read. When the game-data database is unreachable and no memo exists, the
// long-lived fallback key supplies the last stamp a live read ever resolved, so
// warm cache generations keep serving through a database outage — but such a
// stamp is NOT confirmed and must never answer a conditional 304.
func ResolveGameDataStamp(
	ctx context.Context,
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	server harukiUtils.SupportedDataUploadServer,
	dataType harukiUtils.UploadDataType,
	userID int64,
) (stamp int64, confirmed bool, err error) {
	if apiHelper == nil || apiHelper.DBManager == nil {
		return 0, false, fmt.Errorf("db manager is nil")
	}
	memoKey := harukiRedis.BuildGameDataStampMemoKey(string(server), string(dataType), userID)
	if apiHelper.DBManager.Redis != nil {
		if raw, found, rErr := apiHelper.DBManager.Redis.GetRawCache(ctx, memoKey); rErr == nil && found {
			if parsed, pErr := strconv.ParseInt(raw, 10, 64); pErr == nil {
				return parsed, true, nil
			}
			// A corrupt memo falls through to the database and is overwritten below.
		}
	}
	current, found, mErr := readUploadTime(ctx, apiHelper, server, dataType, userID)
	if mErr != nil {
		return resolveStampFallback(ctx, apiHelper, server, dataType, userID, mErr)
	}
	if !found {
		current = 0
	}
	if apiHelper.DBManager.Redis != nil {
		// Best-effort: a failed memo write only means the next read resolves
		// from the database again.
		value := strconv.FormatInt(current, 10)
		if wErr := apiHelper.DBManager.Redis.SetRawCache(ctx, memoKey, value, harukiRedis.GameDataStampMemoTTL); wErr != nil {
			harukiLogger.Warnf("Failed to write game data stamp memo %s: %v", memoKey, wErr)
		}
		fallbackKey := harukiRedis.BuildGameDataStampFallbackKey(string(server), string(dataType), userID)
		if wErr := apiHelper.DBManager.Redis.SetRawCache(ctx, fallbackKey, value, harukiRedis.GameDataCacheTTL); wErr != nil {
			harukiLogger.Warnf("Failed to write game data stamp fallback %s: %v", fallbackKey, wErr)
		}
	}
	return current, true, nil
}

// resolveStampFallback serves the last stamp a live read ever resolved when
// the game-data database is unreachable, so existing cache generations stay
// servable through an outage. The stamp is unconfirmed; when no fallback exists the original
// resolve error surfaces and the full path owns the failure.
func resolveStampFallback(
	ctx context.Context,
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	server harukiUtils.SupportedDataUploadServer,
	dataType harukiUtils.UploadDataType,
	userID int64,
	cause error,
) (int64, bool, error) {
	if apiHelper.DBManager.Redis != nil {
		fallbackKey := harukiRedis.BuildGameDataStampFallbackKey(string(server), string(dataType), userID)
		if raw, found, rErr := apiHelper.DBManager.Redis.GetRawCache(ctx, fallbackKey); rErr == nil && found {
			if parsed, pErr := strconv.ParseInt(raw, 10, 64); pErr == nil {
				return parsed, false, nil
			}
		}
	}
	return 0, false, cause
}

// ConfirmGameDataCacheWrite fences a singleflight leader's cache write: the
// write is allowed only when the generation's second has fully elapsed and a
// fresh database read — bypassing the memo — still reports the same stamp, so a
// leader racing an upload with a NEWER stamp can never resurrect an
// already-cleared generation. It cannot detect a same-stamp collision (two
// uploads minted in one wall-clock second where the body was read between
// their persists — indistinguishable by stamp comparison); that residual class
// is bounded instead by FreshGenerationCacheTTL at the write site. Skipping
// the write is always safe: the next miss simply rematerializes.
func ConfirmGameDataCacheWrite(
	ctx context.Context,
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	server harukiUtils.SupportedDataUploadServer,
	dataType harukiUtils.UploadDataType,
	userID int64,
	stamp int64,
) bool {
	if stamp <= 0 || stamp >= time.Now().Unix() {
		return false
	}
	if apiHelper == nil || apiHelper.DBManager == nil {
		return false
	}
	current, found, err := readUploadTime(ctx, apiHelper, server, dataType, userID)
	return err == nil && found && current == stamp
}

// GameDataCacheWriteTTL picks the retention for a fenced cache write: keyed
// (?key=) bodies get the shorter inflation-capping horizon, and bodies written
// while their generation is still young get FreshGenerationCacheTTL so a
// same-stamp collision (see ConfirmGameDataCacheWrite) self-heals within
// minutes instead of persisting for the full TTL.
func GameDataCacheWriteTTL(requestKey string, stamp int64) time.Duration {
	ttl := harukiRedis.GameDataCacheTTL
	if requestKey != "" {
		ttl = harukiRedis.KeyedGameDataCacheTTL
	}
	if time.Now().Unix()-stamp < int64(harukiRedis.FreshGenerationWindow/time.Second) {
		ttl = harukiRedis.FreshGenerationCacheTTL
	}
	return ttl
}

// PublicAllowlistDigest fingerprints the public-API suite key allowlist for
// use as a cache-key segment: suite bodies on the public and OAuth2 surfaces
// are shaped by the allowlist, so an allowlist edit must move readers to new
// entries — the document stamp alone cannot express that change, and an
// in-flight leader could otherwise re-cache an old-allowlist body after the
// admin-side namespace wipe.
func PublicAllowlistDigest(allowedKeys []string) string {
	sum := md5.Sum([]byte(strings.Join(allowedKeys, ",")))
	return hex.EncodeToString(sum[:8])
}

// readUploadTime resolves the generation stamp from the game-data PostgreSQL
// store (DBManager.GameData, utils/database/gamedata).
//
// A missing store is an ERROR, never a bare false: a silent false would make
// the write fence refuse every write and leave the response cache permanently
// empty with NOTHING reporting it, because an empty cache is indistinguishable
// from a cold one. (The MongoDB-era callers had exactly that bug.)
func readUploadTime(
	ctx context.Context,
	apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	server harukiUtils.SupportedDataUploadServer,
	dataType harukiUtils.UploadDataType,
	userID int64,
) (int64, bool, error) {
	gd := apiHelper.DBManager.GameData
	if gd == nil {
		return 0, false, fmt.Errorf("no game data store is configured")
	}
	collection := "mysekai"
	if dataType == harukiUtils.UploadDataTypeSuite {
		collection = "suite"
	}
	return gd.StoreFor(collection).UploadTime(ctx, userID, string(server))
}

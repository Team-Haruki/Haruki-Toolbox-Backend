package data

import (
	"context"
	"fmt"
	"strings"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"

	"github.com/gofiber/fiber/v3"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// PublishedGameDataCacheVersion separates entries written before the response
// size limit from entries known to fit it.
const PublishedGameDataCacheVersion = ":bounded-v1"

var publishedReadSlots = make(chan struct{}, 4)

// ReservePublishedGameDataRead bounds concurrent reads, cache decoding, and
// rendering shared by the public and OAuth game-data endpoints.
func ReservePublishedGameDataRead() (func(), bool) {
	select {
	case publishedReadSlots <- struct{}{}:
		return func() { <-publishedReadSlots }, true
	default:
		return nil, false
	}
}

// ValidateGameDataKeySelection bounds selection cost before reading or
// rendering game data. Duplicate fields could expand a small stored row.
func ValidateGameDataKeySelection(raw string) error {
	if raw == "" {
		return nil
	}
	if len(raw) > 8192 || strings.Count(raw, ",") >= 256 {
		return fiber.NewError(fiber.StatusBadRequest, "too many game data keys")
	}
	seen := make(map[string]struct{})
	for key := range strings.SplitSeq(raw, ",") {
		if _, exists := seen[key]; exists {
			return fiber.NewError(fiber.StatusBadRequest, "duplicate game data key")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func compactFieldName(key string) string {
	if len(key) == 0 {
		return ""
	}
	return fmt.Sprintf("compact%s%s", string(key[0]-32), key[1:])
}

func buildSuiteProjection(keys []string) bson.M {
	proj := bson.M{"_id": 0}
	for _, key := range keys {
		if key == "userGamedata" {
			for _, field := range userGamedataAllowedFields {
				proj["userGamedata."+field] = 1
			}
		} else {
			proj[key] = 1
			proj[compactFieldName(key)] = 1
		}
	}
	return proj
}

func buildMysekaiProjection(keys []string) bson.M {
	if len(keys) == 0 {
		return bson.M{"_id": 0, "server": 0}
	}
	proj := bson.M{"_id": 0}
	for _, key := range keys {
		proj[key] = 1
	}
	return proj
}

// InvalidSuiteRequestKey returns the first requested suite key the allowlist
// rejects. The conditional (?known_upload_time) path uses it as a gate so a
// request the full path would 400 is never answered with a 304.
func InvalidSuiteRequestKey(requestKey string, allowedKeySet map[string]struct{}) (string, bool) {
	if requestKey == "" {
		return "", false
	}
	for _, key := range strings.Split(requestKey, ",") {
		if key == "userGamedata" {
			continue
		}
		if _, ok := allowedKeySet[key]; !ok {
			return key, true
		}
	}
	return "", false
}

// InvalidMysekaiRequestKey returns the first requested mysekai key that is
// empty or contains `.` or `$`. Those were dotted projection paths and
// operators under the retired MongoDB store; the PostgreSQL read path keeps
// rejecting them so the accepted key set is unchanged and a caller cannot
// probe nested document structure.
func InvalidMysekaiRequestKey(requestKey string) (string, bool) {
	if requestKey == "" {
		return "", false
	}
	for _, key := range strings.Split(requestKey, ",") {
		if strings.TrimSpace(key) == "" || strings.ContainsAny(key, ".$") {
			return key, true
		}
	}
	return "", false
}

// HandleSuiteRequest takes an explicit ctx (rather than deriving one from the
// fiber request) so callers can bound the game-data PostgreSQL read with a
// deadline — Fiber v3 request contexts carry none.
func HandleSuiteRequest(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID int64, server harukiUtils.SupportedDataUploadServer, requestKey string, allowedKeySet map[string]struct{}, allowedKeys []string) (any, error) {
	if err := ValidateGameDataKeySelection(requestKey); err != nil {
		return nil, err
	}
	var keys []string
	if requestKey == "" {
		keys = allowedKeys
	} else {
		if key, invalid := InvalidSuiteRequestKey(requestKey, allowedKeySet); invalid {
			return nil, fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("Invalid request key: %s", key))
		}
		keys = strings.Split(requestKey, ",")
	}

	return suiteBodyFromPostgres(ctx, apiHelper.DBManager.GameData.Suite(), userID, server, keys,
		requestKey != "" && len(keys) == 1)
}

// HandleMysekaiRequest takes an explicit ctx for the same reason as
// HandleSuiteRequest: the caller owns the read deadline.
func HandleMysekaiRequest(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID int64, server harukiUtils.SupportedDataUploadServer, requestKey string, allowedKeys []string) (any, error) {
	keys, err := mysekaiAllowedProjection(requestKey, allowedKeys)
	if err != nil {
		return nil, err
	}
	return mysekaiBodyFromPostgres(ctx, apiHelper.DBManager.GameData.Mysekai(), userID, server, keys)
}

func mysekaiAllowedProjection(requestKey string, allowedKeys []string) ([]string, error) {
	if err := ValidateGameDataKeySelection(requestKey); err != nil {
		return nil, err
	}
	allowed := make(map[string]struct{}, len(allowedKeys))
	var projection []string
	for _, key := range allowedKeys {
		if _, invalid := InvalidMysekaiRequestKey(key); !invalid && key != "" && !strings.Contains(key, ",") {
			if _, found := allowed[key]; !found {
				allowed[key] = struct{}{}
				projection = append(projection, key)
			}
		}
	}
	if requestKey != "" {
		if key, invalid := InvalidMysekaiRequestKey(requestKey); invalid {
			return nil, fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("Invalid request key: %s", key))
		}
		keys := strings.Split(requestKey, ",")
		for _, key := range keys {
			if _, ok := allowed[key]; !ok {
				return nil, fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("Invalid request key: %s", key))
			}
		}
		return keys, nil
	}
	if len(projection) == 0 {
		return nil, fiber.NewError(fiber.StatusNotFound, "Player data not found.")
	}
	return projection, nil
}

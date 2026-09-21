package data

import (
	"context"
	"fmt"
	"strings"

	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"

	"github.com/gofiber/fiber/v3"
	"go.mongodb.org/mongo-driver/v2/bson"
)

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
// empty or that Mongo would treat as a dotted projection path or operator, so
// a caller cannot probe nested document structure.
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
// fiber request) so callers can bound the Mongo read with a deadline — Fiber v3
// request contexts carry none.
func HandleSuiteRequest(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID int64, server harukiUtils.SupportedDataUploadServer, requestKey string, allowedKeySet map[string]struct{}, allowedKeys []string) (any, error) {
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
func HandleMysekaiRequest(ctx context.Context, apiHelper *harukiAPIHelper.HarukiToolboxRouterHelpers, userID int64, server harukiUtils.SupportedDataUploadServer, requestKey string) (any, error) {
	var keys []string
	if requestKey != "" {
		if key, invalid := InvalidMysekaiRequestKey(requestKey); invalid {
			return nil, fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("Invalid request key: %s", key))
		}
		keys = strings.Split(requestKey, ",")
	}

	return mysekaiBodyFromPostgres(ctx, apiHelper.DBManager.GameData.Mysekai(), userID, server, keys)
}

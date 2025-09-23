package upload

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	harukiUtils "haruki-suite/utils"
	harukiDataHandler "haruki-suite/utils/handler"
	harukiLogger "haruki-suite/utils/logger"
	harukiMongo "haruki-suite/utils/mongo"
	harukiRedis "haruki-suite/utils/redis"
	"net/http"
	"strconv"

	"github.com/redis/go-redis/v9"
)

type CachePath struct {
	Namespace   string
	Path        string
	QueryString string
}

func GetClearCachePaths(server string, dataType string, userID int64) []CachePath {
	return []CachePath{
		{
			Namespace: "public_access",
			Path:      fmt.Sprintf("/public/%s/%s/%d", server, dataType, userID),
		},
		{
			Namespace:   "public_access",
			Path:        fmt.Sprintf("/public/%s/%s/%d", server, dataType, userID),
			QueryString: "key=upload_time",
		},
	}
}
func HandleUpload(ctx context.Context, data []byte, server string, policy string, mongoManager *harukiMongo.MongoDBManager, redisClient *redis.Client, dataType string, userID int64) (*harukiUtils.HandleDataResult, error) {
	handler := &harukiDataHandler.DataHandler{MongoManager: mongoManager, HTTPClient: &http.Client{}, Logger: *harukiLogger.NewLogger("DataHandler", "DEBUG", nil)}
	result, err := handler.HandleAndUpdateData(ctx, data, harukiUtils.SupportedDataUploadServer(server), harukiUtils.UploadPolicy(policy), harukiUtils.UploadDataType(dataType), &userID)
	if err != nil {
		return result, err
	}

	if userID == 0 && result.UserID != nil && *result.UserID != 0 {
		userID = *result.UserID
	}

	if result.Status != nil && *result.Status != 200 {
		return result, errors.New("upload failed with status: " + strconv.Itoa(*result.Status))
	}

	paths := GetClearCachePaths(dataType, server, userID)
	for _, path := range paths {
		sum := md5.Sum([]byte(path.QueryString))
		queryHash := hex.EncodeToString(sum[:])
		if err := harukiRedis.DeleteCache(ctx, redisClient, fmt.Sprintf("%s:%s:query=%s", path.Namespace, path.Path, queryHash)); err != nil {
			// Log error but continue clearing other cache paths
		}
	}

	return result, nil
}

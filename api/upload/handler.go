package upload

import (
	"context"
	"errors"
	harukiUtils "haruki-suite/utils"
	harukiDataHandler "haruki-suite/utils/handler"
	harukiLogger "haruki-suite/utils/logger"
	harukiMongo "haruki-suite/utils/mongo"
	harukiRedis "haruki-suite/utils/redis"
	"strconv"

	"github.com/go-resty/resty/v2"
	"github.com/redis/go-redis/v9"
)

func HandleUpload(ctx context.Context, data []byte, server string, policy string, mongoManager *harukiMongo.MongoDBManager, redisClient *redis.Client, dataType string, userID int64) (*harukiUtils.HandleDataResult, error) {
	handler := &harukiDataHandler.DataHandler{MongoManager: mongoManager, RestyClient: resty.New(), Logger: *harukiLogger.NewLogger("SekaiDataHandler", "DEBUG", nil)}
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

	if err = harukiRedis.ClearCache(ctx, redisClient, dataType, server, userID); err != nil {
		return result, err
	}

	return result, nil
}

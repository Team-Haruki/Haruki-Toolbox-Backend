package handler

import (
	"context"
	"fmt"
	"haruki-suite/utils"
	harukiMongo "haruki-suite/utils/database/mongo"
	harukiHttp "haruki-suite/utils/http"
	harukiLogger "haruki-suite/utils/logger"
	harukiSekai "haruki-suite/utils/sekai"
	harukiVersion "haruki-suite/version"
	"strconv"
	"strings"
	"sync"
	"time"

	"encoding/json"
)

type DataHandler struct {
	MongoManager *harukiMongo.MongoDBManager
	HttpClient   *harukiHttp.Client
	Logger       *harukiLogger.Logger
}

func (h *DataHandler) PreHandleData(data map[string]interface{}, userID int64, policy utils.UploadPolicy, server utils.SupportedDataUploadServer) map[string]interface{} {
	data["upload_time"] = time.Now().Unix()
	data["policy"] = string(policy)
	data["_id"] = userID
	data["server"] = string(server)
	return data
}

func (h *DataHandler) HandleAndUpdateData(ctx context.Context, raw []byte, server utils.SupportedDataUploadServer, policy utils.UploadPolicy, dataType utils.UploadDataType, userID *int64) (*utils.HandleDataResult, error) {
	unpacked, err := harukiSekai.Unpack(raw, server)
	if err != nil {
		h.Logger.Errorf("unpack failed: %v", err)
		return nil, err
	}

	unpackedMap, ok := unpacked.(map[string]interface{})
	if !ok {
		h.Logger.Errorf("unpack returned unexpected type %T", unpacked)
		return nil, fmt.Errorf("invalid unpacked data type")
	}

	if status, ok := unpackedMap["httpStatus"]; ok {
		errCode, _ := unpackedMap["errorCode"].(string)
		var statusCode int
		switch v := status.(type) {
		case float64:
			statusCode = int(v)
		case int:
			statusCode = v
		case int32:
			statusCode = int(v)
		case int64:
			statusCode = int(v)
		case uint16:
			statusCode = int(v)
		case uint32:
			statusCode = int(v)
		case uint64:
			statusCode = int(v)
		case json.Number:
			if i64, err := v.Int64(); err == nil {
				statusCode = int(i64)
			}
		default:
			h.Logger.Debugf("unexpected httpStatus type: %T, value: %v", v, v)
		}
		return &utils.HandleDataResult{
			Status:       &statusCode,
			ErrorMessage: &errCode,
		}, fmt.Errorf("data retrieve error")
	}

	if userID == nil {
		var extracted int64 = 0
		if gameData, ok := unpackedMap["userGamedata"].(map[string]interface{}); ok {
			switch v := gameData["userId"].(type) {
			case json.Number:
				if id64, err := v.Int64(); err == nil {
					extracted = id64
				} else if u64, err := strconv.ParseUint(v.String(), 10, 64); err == nil {
					extracted = int64(u64)
				}
			case string:
				if u64, err := strconv.ParseUint(v, 10, 64); err == nil {
					extracted = int64(u64)
				}
			case float64:
				extracted = int64(v)
			case int64:
				extracted = v
			case uint64:
				extracted = int64(v)
			default:
				h.Logger.Debugf("userId raw type: %T, value: %v", v, v)
			}
		}

		if extracted == 0 {
			return nil, fmt.Errorf("failed to extract userId from unpacked data")
		}
		userID = new(int64)
		*userID = extracted
	}
	if userID == nil {
		return nil, fmt.Errorf("failed to extract userId from unpacked data")
	}

	data := h.PreHandleData(unpackedMap, *userID, policy, server)

	if _, err := h.MongoManager.UpdateData(ctx, string(server), *userID, data, dataType); err != nil {
		return nil, err
	}

	if policy == utils.UploadPolicyPublic {
		go h.CallWebhook(ctx, *userID, server, dataType)
	}

	return &utils.HandleDataResult{UserID: userID}, nil
}

func (h *DataHandler) CallbackWebhookAPI(ctx context.Context, url, bearer string) {
	h.Logger.Infof("Calling back WebHook API: %s", url)

	headers := map[string]string{
		"User-Agent": fmt.Sprintf("Haruki-Suite/%s", harukiVersion.Version),
	}
	if bearer != "" {
		headers["Authorization"] = "Bearer " + bearer
	}

	statusCode, _, _, err := h.HttpClient.Request(ctx, "POST", url, headers, nil)
	if err != nil {
		h.Logger.Errorf("WebHook API call failed: %v", err)
		return
	}

	if statusCode == 200 {
		h.Logger.Infof("Called back WebHook API %s successfully.", url)
	} else {
		h.Logger.Errorf("Called back WebHook API %s failed, status code: %d", url, statusCode)
	}
}

func (h *DataHandler) CallWebhook(ctx context.Context, userID int64, server utils.SupportedDataUploadServer, dataType utils.UploadDataType) {
	callbacks, err := h.MongoManager.GetWebhookPushAPI(ctx, userID, string(server), string(dataType))

	if err != nil || len(callbacks) == 0 {
		return
	}

	var wg sync.WaitGroup
	for _, cb := range callbacks {
		url := cb["callback_url"].(string)
		url = strings.ReplaceAll(url, "{user_id}", fmt.Sprint(userID))
		url = strings.ReplaceAll(url, "{server}", string(server))
		url = strings.ReplaceAll(url, "{data_type}", string(dataType))
		bearer, _ := cb["Bearer"].(string)

		wg.Add(1)
		go func(u, b string) {
			defer wg.Done()
			h.CallbackWebhookAPI(ctx, u, b)
		}(url, bearer)
	}
	wg.Wait()
}

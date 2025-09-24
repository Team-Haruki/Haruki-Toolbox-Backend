package handler

import (
	"context"
	"encoding/json"
	"fmt"
	harukiUtils "haruki-suite/utils"
	harukiLogger "haruki-suite/utils/logger"
	harukiMongo "haruki-suite/utils/mongo"
	harukiSekai "haruki-suite/utils/sekai"
	harukiVersion "haruki-suite/version"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
)

type DataHandler struct {
	MongoManager *harukiMongo.MongoDBManager
	RestyClient  *resty.Client
	Logger       *harukiLogger.Logger
}

func (h *DataHandler) PreHandleData(data map[string]interface{}, userID int64, policy harukiUtils.UploadPolicy, server harukiUtils.SupportedDataUploadServer) map[string]interface{} {
	data["upload_time"] = time.Now().Unix()
	data["policy"] = string(policy)
	data["_id"] = userID
	data["server"] = string(server)
	return data
}

func (h *DataHandler) HandleAndUpdateData(ctx context.Context, raw []byte, server harukiUtils.SupportedDataUploadServer, policy harukiUtils.UploadPolicy, dataType harukiUtils.UploadDataType, userID *int64) (*harukiUtils.HandleDataResult, error) {
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
		statusCode := int(status.(float64))
		return &harukiUtils.HandleDataResult{
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
		h.Logger.Debugf("Extracted userId: %d", extracted)
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

	if _, err := h.MongoManager.UpdateData(ctx, *userID, data, dataType); err != nil {
		return nil, err
	}

	if policy == harukiUtils.UploadPolicyPublic {
		go h.CallWebhook(ctx, *userID, server, dataType)
	}

	return &harukiUtils.HandleDataResult{UserID: userID}, nil
}

func (h *DataHandler) CallbackWebhookAPI(ctx context.Context, url, bearer string) {
	h.Logger.Infof("Calling back WebHook API: %s", url)

	request := h.RestyClient.R().
		SetContext(ctx).
		SetHeader("User-Agent", fmt.Sprintf("Haruki-Suite/%s", harukiVersion.Version))
	if bearer != "" {
		request.SetHeader("Authorization", "Bearer "+bearer)
	}

	resp, err := request.Post(url)
	if err != nil {
		h.Logger.Errorf("WebHook API call failed: %v", err)
		return
	}
	io.Copy(io.Discard, resp.RawBody())
	resp.RawBody().Close()

	if resp.StatusCode() == 200 {
		h.Logger.Infof("Called back WebHook API %s successfully.", url)
	} else {
		h.Logger.Errorf("Called back WebHook API %s failed, status code: %d", url, resp.StatusCode())
	}
}

func (h *DataHandler) CallWebhook(ctx context.Context, userID int64, server harukiUtils.SupportedDataUploadServer, dataType harukiUtils.UploadDataType) {
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

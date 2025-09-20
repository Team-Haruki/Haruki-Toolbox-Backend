package handler

import (
	"bytes"
	"context"
	"fmt"
	harukiUtils "haruki-suite/utils"
	harukiLogger "haruki-suite/utils/logger"
	harukiMongo "haruki-suite/utils/mongo"
	harukiSekaiClient "haruki-suite/utils/sekai"
	harukiVersion "haruki-suite/version"
	"io"
	"net/http"
	"sync"
	"time"
)

type DataHandler struct {
	MongoManager *harukiMongo.MongoDBManager
	HTTPClient   *http.Client
	Logger       harukiLogger.Logger
}

type HandleDataResult struct {
	UserID       int64
	Status       int
	ErrorMessage string
}

func (h *DataHandler) PreHandleData(data map[string]interface{}, userID int64, policy harukiUtils.UploadPolicy, server harukiUtils.SupportedDataUploadServer) map[string]interface{} {
	data["upload_time"] = time.Now().Unix()
	data["policy"] = string(policy)
	data["_id"] = userID
	data["server"] = string(server)
	return data
}

func (h *DataHandler) HandleAndUpdateData(ctx context.Context, raw []byte, server harukiUtils.SupportedDataUploadServer, policy harukiUtils.UploadPolicy, dataType harukiUtils.UploadDataType, userID int64) (*HandleDataResult, error) {
	unpacked, err := harukiSekaiClient.Unpack(raw, server)
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
		return &HandleDataResult{
			Status:       int(status.(float64)),
			ErrorMessage: errCode,
		}, nil
	}

	if userID == 0 {
		if gameData, ok := unpackedMap["userGamedata"].(map[string]interface{}); ok {
			if id, ok := gameData["userId"].(float64); ok {
				userID = int64(id)
			}
		}
	}

	data := h.PreHandleData(unpackedMap, userID, policy, server)

	if _, err := h.MongoManager.UpdateData(ctx, userID, data, dataType); err != nil {
		return nil, err
	}

	if policy == harukiUtils.UploadPolicyPublic {
		go h.CallWebhook(ctx, userID, string(server), dataType)
	}

	return &HandleDataResult{UserID: userID}, nil
}

func (h *DataHandler) CallbackWebhookAPI(ctx context.Context, url, bearer string) {
	h.Logger.Infof("Calling back WebHook API: %s", url)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(nil))
	req.Header.Set("User-Agent", fmt.Sprintf("Haruki-Suite/%s", harukiVersion.Version))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := h.HTTPClient.Do(req)
	if err != nil {
		h.Logger.Errorf("WebHook API call failed: %v", err)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode == 200 {
		h.Logger.Infof("Called back WebHook API %s successfully.", url)
	} else {
		h.Logger.Errorf("Called back WebHook API %s failed, status code: %d", url, resp.StatusCode)
	}
}

func (h *DataHandler) CallWebhook(ctx context.Context, userID int64, server string, dataType harukiUtils.UploadDataType) {
	callbacks, err := h.MongoManager.GetWebhookPushAPI(ctx, userID, server, string(dataType))
	if err != nil || len(callbacks) == 0 {
		return
	}

	var wg sync.WaitGroup
	for _, cb := range callbacks {
		url := fmt.Sprintf(cb["callback_url"].(string), userID, server, dataType)
		bearer, _ := cb["bearer"].(string)

		wg.Add(1)
		go func(u, b string) {
			defer wg.Done()
			h.CallbackWebhookAPI(ctx, u, b)
		}(url, bearer)
	}
	wg.Wait()
}

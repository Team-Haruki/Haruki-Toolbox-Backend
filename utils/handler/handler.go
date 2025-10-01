package handler

import (
	"context"
	"fmt"
	harukiConfig "haruki-suite/config"
	"haruki-suite/utils"
	"haruki-suite/utils/database"
	harukiHttp "haruki-suite/utils/http"
	harukiLogger "haruki-suite/utils/logger"
	harukiSekai "haruki-suite/utils/sekai"
	"haruki-suite/utils/sekaiapi"
	harukiVersion "haruki-suite/version"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"encoding/json"
)

type DataHandler struct {
	DBManager      *database.HarukiToolboxDBManager
	SeakiAPIClient *sekaiapi.HarukiSekaiAPIClient
	HttpClient     *harukiHttp.Client
	Logger         *harukiLogger.Logger
}

func cleanSuite(suite map[string]interface{}) map[string]interface{} {
	removeKeys := harukiConfig.Cfg.SekaiClient.SuiteRemoveKeys
	for _, key := range removeKeys {
		if _, ok := suite[key]; ok {
			suite[key] = []interface{}{}
		}
	}
	return suite
}
func (h *DataHandler) PreHandleData(data map[string]interface{}, expectedUserID *int64, parsedUserID *int64, server utils.SupportedDataUploadServer, dataType utils.UploadDataType) (map[string]interface{}, error) {
	if dataType == utils.UploadDataTypeSuite && parsedUserID != nil && expectedUserID != parsedUserID {
		return nil, fmt.Errorf("invalid userID: %d, expected: %d", parsedUserID, expectedUserID)
	}
	if dataType == utils.UploadDataTypeMysekai {
		updatedResources, ok := data["updatedResources"].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid data: missing updatedResources")
		}

		photos, ok := updatedResources["userMysekaiPhotos"].([]interface{})
		if !ok || len(photos) == 0 {
			return nil, fmt.Errorf("no userMysekaiPhotos found")
		}

		firstPhoto, ok := photos[0].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid photo data")
		}

		imagePath, _ := firstPhoto["imagePath"].(string)
		if imagePath == "" {
			return nil, fmt.Errorf("missing imagePath")
		}

		hashPattern := regexp.MustCompile(`^[a-f0-9]{64}/[a-f0-9]{64}$`)
		if hashPattern.MatchString(imagePath) {
			if server == utils.SupportedDataUploadServerJP || server == utils.SupportedDataUploadServerEN {
			} else {
				return nil, fmt.Errorf("invalid server: %s", server)
			}
		} else {
			uidPattern := regexp.MustCompile(`^(\d+)_([0-9a-fA-F-]{36})$`)
			matches := uidPattern.FindStringSubmatch(imagePath)
			if len(matches) == 3 {
				uid := matches[1]
				uidInt, err := strconv.ParseInt(uid, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid uid format")
				}
				if uidInt != *expectedUserID {
					return nil, fmt.Errorf("uid does not match expectedUserID")
				}
				data["uid"] = uid
				ok, body, err := h.SeakiAPIClient.GetUserProfile(uid, string(server))
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("sekai api is unavailable")
				}
				if body == nil {
					return nil, fmt.Errorf("illegal request")
				}
			} else {
				return nil, fmt.Errorf("invalid imagePath format")
			}
		}
	}
	if dataType == utils.UploadDataTypeSuite {
		data = cleanSuite(data)
	}
	data["upload_time"] = time.Now().Unix()
	data["_id"] = expectedUserID
	data["server"] = string(server)
	return data, nil
}

func (h *DataHandler) HandleAndUpdateData(ctx context.Context, raw []byte, server utils.SupportedDataUploadServer, isPublicAPI bool, dataType utils.UploadDataType, expectedUserID *int64) (*utils.HandleDataResult, error) {
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

	var extractedUserID *int64 = nil
	if gameData, ok := unpackedMap["userGamedata"].(map[string]interface{}); ok {
		switch v := gameData["userId"].(type) {
		case json.Number:
			if id64, err := v.Int64(); err == nil {
				tmp := id64
				extractedUserID = &tmp
			} else if u64, err := strconv.ParseUint(v.String(), 10, 64); err == nil {
				tmp := int64(u64)
				extractedUserID = &tmp
			}
		case string:
			if u64, err := strconv.ParseUint(v, 10, 64); err == nil {
				tmp := int64(u64)
				extractedUserID = &tmp
			}
		case float64:
			tmp := int64(v)
			extractedUserID = &tmp
		case int64:
			tmp := v
			extractedUserID = &tmp
		case uint64:
			tmp := int64(v)
			extractedUserID = &tmp
		default:
			h.Logger.Debugf("userId raw type: %T, value: %v", v, v)
		}

	}

	data, err := h.PreHandleData(unpackedMap, expectedUserID, extractedUserID, server, dataType)
	if err != nil {
		return nil, err
	}

	if _, err := h.DBManager.Mongo.UpdateData(ctx, string(server), *expectedUserID, data, dataType); err != nil {
		return nil, err
	}

	if isPublicAPI {
		go h.CallWebhook(ctx, *expectedUserID, server, dataType)
	}

	return &utils.HandleDataResult{UserID: expectedUserID}, nil
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
	callbacks, err := h.DBManager.Mongo.GetWebhookPushAPI(ctx, userID, string(server), string(dataType))

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

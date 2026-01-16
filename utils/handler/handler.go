package handler

import (
	"context"
	"fmt"
	harukiConfig "haruki-suite/config"
	"haruki-suite/utils"
	apiHelper "haruki-suite/utils/api"
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
	SekaiAPIClient *sekaiapi.HarukiSekaiAPIClient
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
	if err := validateUserIDMatch(expectedUserID, parsedUserID, dataType); err != nil {
		return nil, err
	}

	if dataType == utils.UploadDataTypeMysekai {
		if err := h.validateMysekaiData(data, expectedUserID, server); err != nil {
			return nil, err
		}
	}

	if dataType == utils.UploadDataTypeSuite {
		if err := validateSuiteData(data); err != nil {
			return nil, err
		}
		data = cleanSuite(data)
	}

	if dataType == utils.UploadDataTypeMysekaiBirthdayParty {
		if err := validateBirthdayPartyData(data); err != nil {
			return nil, err
		}
		data = extractBirthdayPartyData(data)
	}

	data["upload_time"] = time.Now().Unix()
	data["_id"] = expectedUserID
	data["server"] = string(server)
	return data, nil
}

func validateUserIDMatch(expectedUserID, parsedUserID *int64, dataType utils.UploadDataType) error {
	if dataType == utils.UploadDataTypeSuite && parsedUserID != nil && expectedUserID != nil && *expectedUserID != *parsedUserID {
		return fmt.Errorf("invalid userID: %s, expected: %s", strconv.FormatInt(*parsedUserID, 10), strconv.FormatInt(*expectedUserID, 10))
	}
	return nil
}

func (h *DataHandler) validateMysekaiData(data map[string]interface{}, expectedUserID *int64, server utils.SupportedDataUploadServer) error {
	updatedResources, ok := data["updatedResources"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid data: missing updatedResources")
	}

	photos, ok := updatedResources["userMysekaiPhotos"].([]interface{})
	if !ok || len(photos) == 0 {
		return fmt.Errorf("no userMysekaiPhotos found, it seems you may not have taken a photo yet")
	}

	firstPhoto, ok := photos[0].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid photo data")
	}

	imagePath, ok := firstPhoto["imagePath"].(string)
	if imagePath == "" || !ok {
		return fmt.Errorf("missing imagePath")
	}

	return h.validateImagePath(imagePath, expectedUserID, server)
}

func (h *DataHandler) validateImagePath(imagePath string, expectedUserID *int64, server utils.SupportedDataUploadServer) error {
	if server == utils.SupportedDataUploadServerJP || server == utils.SupportedDataUploadServerEN {
		return validateJPENImagePath(imagePath, server)
	}
	return h.validateOtherServerImagePath(imagePath, expectedUserID, server)
}

func validateJPENImagePath(imagePath string, server utils.SupportedDataUploadServer) error {
	hashPattern := regexp.MustCompile(`^[a-f0-9]{64}/[a-f0-9]{64}$`)
	if !hashPattern.MatchString(imagePath) {
		return fmt.Errorf("invalid server: %s", server)
	}
	return nil
}

func (h *DataHandler) validateOtherServerImagePath(imagePath string, expectedUserID *int64, server utils.SupportedDataUploadServer) error {
	uidPattern := regexp.MustCompile(`^(\d+)_([0-9a-fA-F-]{36})$`)
	matches := uidPattern.FindStringSubmatch(imagePath)
	if len(matches) != 3 {
		return fmt.Errorf("invalid imagePath format")
	}

	uid := matches[1]
	uidInt, err := strconv.ParseInt(uid, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid uid format")
	}

	if expectedUserID == nil {
		return fmt.Errorf("expected user ID is nil")
	}

	if uidInt != *expectedUserID {
		return fmt.Errorf("userId %s does not match expected UserId %d", uid, *expectedUserID)
	}

	return h.verifyGameAccountExists(uid, server)
}

func (h *DataHandler) verifyGameAccountExists(uid string, server utils.SupportedDataUploadServer) error {
	resultInfo, _, err := h.SekaiAPIClient.GetUserProfile(uid, string(server))
	if resultInfo == nil {
		if err != nil {
			return err
		}
		return fmt.Errorf("failed to get user profile")
	}
	if !resultInfo.ServerAvailable {
		return fmt.Errorf("sekai api is unavailable")
	}
	if !resultInfo.AccountExists {
		return fmt.Errorf("game account not found")
	}
	return nil
}

func validateSuiteData(data map[string]interface{}) error {
	_, ok := data["userGamedata"]
	_, ok2 := data["userProfile"]
	if !ok && !ok2 {
		return fmt.Errorf("invalid data, it seems you may have uploaded a wrong suite data")
	}
	return nil
}

func validateBirthdayPartyData(data map[string]interface{}) error {
	updatedResources, ok := data["updatedResources"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid data: missing updatedResources")
	}

	harvestMaps, ok := updatedResources["userMysekaiHarvestMaps"]
	if !ok || harvestMaps == nil {
		return fmt.Errorf("no userMysekaiHarvestMaps found, it seems you may not have participated in the birthday party event yet")
	}

	return nil
}

func extractBirthdayPartyData(data map[string]interface{}) map[string]interface{} {
	updatedResources, _ := data["updatedResources"].(map[string]interface{})
	harvestMaps := updatedResources["userMysekaiHarvestMaps"]

	return map[string]interface{}{
		"updatedResources": map[string]interface{}{
			"userMysekaiHarvestMaps": harvestMaps,
		},
	}
}

func (h *DataHandler) HandleAndUpdateData(ctx context.Context, raw []byte, server utils.SupportedDataUploadServer, isPublicAPI bool, dataType utils.UploadDataType, expectedUserID *int64, settings apiHelper.HarukiToolboxGameAccountPrivacySettings) (*utils.HandleDataResult, error) {
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

	if result := h.checkForHTTPError(unpackedMap); result != nil {
		return result, fmt.Errorf("data retrieve error")
	}

	extractedUserID := extractUserIDFromGameData(unpackedMap, h.Logger)

	data, err := h.PreHandleData(unpackedMap, expectedUserID, extractedUserID, server, dataType)
	if err != nil {
		return nil, err
	}

	if dataType != utils.UploadDataTypeMysekaiBirthdayParty {
		go DataSyncer(*expectedUserID, server, dataType, raw, settings)
	} else {
		packedBody, err := harukiSekai.Pack(data, server)
		if err != nil {
			h.Logger.Errorf("pack birthday party data failed: %v", err)
		} else {
			go DataSyncer(*expectedUserID, server, dataType, packedBody, settings)
		}

	}

	if _, err := h.DBManager.Mongo.UpdateData(ctx, string(server), *expectedUserID, data, dataType); err != nil {
		h.Logger.Errorf("Failed to update mongo data: %v", err)
		return nil, err
	}

	if isPublicAPI {
		go h.CallWebhook(ctx, *expectedUserID, server, dataType)
	}

	return &utils.HandleDataResult{UserID: expectedUserID}, nil
}

func (h *DataHandler) checkForHTTPError(unpackedMap map[string]interface{}) *utils.HandleDataResult {
	status, ok := unpackedMap["httpStatus"]
	if !ok {
		return nil
	}

	errCode, _ := unpackedMap["errorCode"].(string)
	statusCode := convertToStatusCode(status, h.Logger)
	return &utils.HandleDataResult{
		Status:       &statusCode,
		ErrorMessage: &errCode,
	}
}

func convertToStatusCode(status interface{}, logger *harukiLogger.Logger) int {
	switch v := status.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case uint16:
		return int(v)
	case uint32:
		return int(v)
	case uint64:
		return int(v)
	case json.Number:
		if i64, err := v.Int64(); err == nil {
			return int(i64)
		}
	default:
		logger.Debugf("unexpected httpStatus type: %T, value: %v", v, v)
	}
	return 0
}

func extractUserIDFromGameData(unpackedMap map[string]interface{}, logger *harukiLogger.Logger) *int64 {
	gameData, ok := unpackedMap["userGamedata"].(map[string]interface{})
	if !ok {
		return nil
	}

	userIDValue, ok := gameData["userId"]
	if !ok {
		return nil
	}

	return convertToInt64Pointer(userIDValue, logger)
}

func convertToInt64Pointer(value interface{}, logger *harukiLogger.Logger) *int64 {
	switch v := value.(type) {
	case json.Number:
		if id64, err := v.Int64(); err == nil {
			return &id64
		}
		if u64, err := strconv.ParseUint(v.String(), 10, 64); err == nil {
			tmp := int64(u64)
			return &tmp
		}
	case string:
		if u64, err := strconv.ParseUint(v, 10, 64); err == nil {
			tmp := int64(u64)
			return &tmp
		}
	case float64:
		tmp := int64(v)
		return &tmp
	case int64:
		return &v
	case uint64:
		tmp := int64(v)
		return &tmp
	default:
		logger.Debugf("userId raw type: %T, value: %v", v, v)
	}
	return nil
}

func (h *DataHandler) CallbackWebhookAPI(ctx context.Context, url, bearer string) {
	h.Logger.Infof("Calling back WebHook API: %s", url)

	headers := map[string]string{
		"User-Agent": fmt.Sprintf("Haruki-Toolbox-Backend/%s", harukiVersion.Version),
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

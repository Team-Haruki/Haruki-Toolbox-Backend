package handler

import (
	"context"
	"fmt"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	apiHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/sekai"
)

func (h *DataHandler) HandleAndUpdateData(
	ctx context.Context,
	raw []byte,
	server utils.SupportedDataUploadServer,
	isPublicAPI bool,
	dataType utils.UploadDataType,
	expectedUserID *int64,
	settings apiHelper.HarukiToolboxGameAccountPrivacySettings,
) (*utils.HandleDataResult, error) {
	unpackedMap, result, err := h.DecodeUploadData(raw, server)
	if err != nil || result != nil {
		return result, err
	}
	parsedUserID, err := extractUserIDFromGameDataWithExpected(unpackedMap, expectedUserID, h.Logger)
	if err != nil {
		return nil, err
	}
	data, err := h.PreHandleData(unpackedMap, expectedUserID, parsedUserID.Value, server, dataType)
	if err != nil {
		return nil, err
	}
	if err := h.PersistUploadData(ctx, data, server, dataType, expectedUserID); err != nil {
		return nil, err
	}
	h.RunUploadFanout(raw, data, server, dataType, expectedUserID, settings, isPublicAPI)
	return &utils.HandleDataResult{UserID: expectedUserID}, nil
}

func (h *DataHandler) DecodeUploadData(raw []byte, server utils.SupportedDataUploadServer) (map[string]any, *utils.HandleDataResult, error) {
	unpacked, err := harukiSekai.Unpack(raw, server)
	if err != nil {
		h.Logger.Errorf("unpack failed: %v", err)
		return nil, nil, err
	}
	unpackedMap, ok := unpacked.(map[string]any)
	if !ok {
		h.Logger.Errorf("unpack returned unexpected type %T", unpacked)
		return nil, nil, fmt.Errorf("invalid unpacked data type")
	}
	if result := h.checkForHTTPError(unpackedMap); result != nil {
		return nil, result, fmt.Errorf("data retrieve error")
	}
	return unpackedMap, nil, nil
}

func (h *DataHandler) ExtractGameUserID(data map[string]any) (ParsedGameUserID, error) {
	return extractUserIDFromGameData(data, h.Logger)
}

func (h *DataHandler) ExtractGameUserIDForExpected(data map[string]any, expectedUserID *int64) (ParsedGameUserID, error) {
	return extractUserIDFromGameDataWithExpected(data, expectedUserID, h.Logger)
}

func (h *DataHandler) PersistUploadData(ctx context.Context, data map[string]any, server utils.SupportedDataUploadServer, dataType utils.UploadDataType, expectedUserID *int64) error {
	if _, err := h.DBManager.Mongo.UpdateData(ctx, string(server), *expectedUserID, data, dataType); err != nil {
		h.Logger.Errorf("Failed to update mongo data: %v", err)
		return err
	}
	return nil
}

func (h *DataHandler) RunUploadFanout(raw []byte, data map[string]any, server utils.SupportedDataUploadServer, dataType utils.UploadDataType, expectedUserID *int64, settings apiHelper.HarukiToolboxGameAccountPrivacySettings, isPublicAPI bool) {
	if dataType == utils.UploadDataTypeMysekaiBirthdayParty && expectedUserID != nil {
		h.ProcessBirthdaySubscriptionAsync(*expectedUserID, server, data)
	}
	if dataType != utils.UploadDataTypeMysekaiBirthdayParty {
		rawCopy := make([]byte, len(raw))
		copy(rawCopy, raw)
		go DataSyncer(*expectedUserID, server, dataType, rawCopy, settings)
	} else {
		packedBody, err := harukiSekai.Pack(data, server)
		if err != nil {
			h.Logger.Errorf("pack birthday party data failed: %v", err)
		} else {
			go DataSyncer(*expectedUserID, server, dataType, packedBody, settings)
		}
	}
	if isPublicAPI {
		go h.CallWebhookAsync(*expectedUserID, server, dataType)
	}
	go h.CallOAuth2WebhookAsync(*expectedUserID, server, dataType)
}

func (h *DataHandler) checkForHTTPError(unpackedMap map[string]any) *utils.HandleDataResult {
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

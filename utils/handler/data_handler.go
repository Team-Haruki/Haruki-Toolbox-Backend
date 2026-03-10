package handler

import (
	"context"
	"fmt"
	"haruki-suite/utils"
	apiHelper "haruki-suite/utils/api"
	harukiSekai "haruki-suite/utils/sekai"
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
	unpacked, err := harukiSekai.Unpack(raw, server)
	if err != nil {
		h.Logger.Errorf("unpack failed: %v", err)
		return nil, err
	}
	unpackedMap, ok := unpacked.(map[string]any)
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
	if _, err := h.DBManager.Mongo.UpdateData(ctx, string(server), *expectedUserID, data, dataType); err != nil {
		h.Logger.Errorf("Failed to update mongo data: %v", err)
		return nil, err
	}
	if isPublicAPI {
		go h.CallWebhook(ctx, *expectedUserID, server, dataType)
	}
	return &utils.HandleDataResult{UserID: expectedUserID}, nil
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

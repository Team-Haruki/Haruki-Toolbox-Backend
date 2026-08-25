package handler

import (
	"context"
	"fmt"
	"sync"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	apiHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
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
	unpacked, err := h.ServerCryptor.Unpack(raw, server)
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
	// Mirror into the PostgreSQL game-data store. MongoDB is authoritative until
	// game_data.read_source flips; after it flips this write is what makes an
	// upload readable. It never fails the upload — see shadowWriteGameData.
	h.shadowWriteGameData(ctx, data, server, dataType, *expectedUserID)
	return nil
}

func (h *DataHandler) RunUploadFanout(raw []byte, data map[string]any, server utils.SupportedDataUploadServer, dataType utils.UploadDataType, expectedUserID *int64, settings apiHelper.HarukiToolboxGameAccountPrivacySettings, isPublicAPI bool) {
	if h == nil || expectedUserID == nil {
		return
	}
	userID := *expectedUserID

	var syncBody []byte
	if dataType != utils.UploadDataTypeMysekaiBirthdayParty {
		syncBody = append([]byte(nil), raw...)
	} else {
		packedBody, err := h.ServerCryptor.Pack(data, server)
		if err != nil {
			h.Logger.Errorf("pack birthday party data failed: %v", err)
		} else {
			syncBody = packedBody
		}
	}

	h.submitBackgroundTask("upload-fanout", func() {
		var fanout sync.WaitGroup
		start := func(name string, task func()) {
			fanout.Add(1)
			go func() {
				defer fanout.Done()
				defer func() {
					if recovered := recover(); recovered != nil && h.Logger != nil {
						h.Logger.Errorf("Upload fanout task %q panicked: %v", name, recovered)
					}
				}()
				task()
			}()
		}

		if dataType == utils.UploadDataTypeMysekaiBirthdayParty {
			start("birthday-subscription", func() {
				h.processBirthdaySubscription(userID, server, data)
			})
		}
		if len(syncBody) > 0 {
			start("data-sync", func() {
				DataSyncer(userID, server, dataType, syncBody, settings, h.ServerCryptor, h.SuiteRestoreService)
			})
		}
		if isPublicAPI {
			start("webhook", func() {
				h.CallWebhookAsync(userID, server, dataType)
			})
		}
		start("oauth2-webhook", func() {
			h.CallOAuth2WebhookAsync(userID, server, dataType)
		})
		fanout.Wait()
	})
}

func (h *DataHandler) submitBackgroundTask(name string, task func()) bool {
	if h == nil || task == nil {
		return false
	}
	if h.BackgroundTasks == nil {
		go task()
		return true
	}
	if h.BackgroundTasks.Go(name, task) {
		return true
	}
	if h.Logger != nil {
		h.Logger.Warnf("Background task %q rejected because application shutdown is draining uploads", name)
	} else {
		harukiLogger.Warnf("Background task %q rejected because application shutdown is draining uploads", name)
	}
	return false
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

package upload

import (
	"context"
	"fmt"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
)

var uploadSemaphore = make(chan struct{}, 10)

const (
	uploadStageBuildContext     = "build_context"
	uploadStageAccountPolicy    = "account_policy"
	uploadStageDecodePayload    = "decode_payload"
	uploadStageValidateIdentity = "validate_payload_identity"
	uploadStagePreprocess       = "preprocess"
	uploadStagePersist          = "persist"
	uploadStageValidateResult   = "validate_result"
)

func HandleUpload(
	ctx context.Context,
	data []byte,
	server harukiUtils.SupportedDataUploadServer,
	dataType harukiUtils.UploadDataType,
	gameUserID *int64,
	userID *string,
	helper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	uploadMethod harukiUtils.UploadMethod,
) (*harukiUtils.HandleDataResult, error) {

	uploadSemaphore <- struct{}{}
	defer func() { <-uploadSemaphore }()

	uploadCtx, err := buildUploadContext(server, dataType, gameUserID, userID, uploadMethod)
	if err != nil {
		return nil, err
	}
	handler := newUploadDataHandler(helper)
	auditWritten := false
	writeUploadAudit := func(success bool, errorMessage *string) {
		if auditWritten {
			return
		}
		dispatchUploadAuditLog(helper, handler.Logger, uploadCtx, success, errorMessage)
		auditWritten = true
	}
	fail := func(stage string, result *harukiUtils.HandleDataResult, err error) (*harukiUtils.HandleDataResult, error) {
		uploadCtx.FailureStage = stage
		if err != nil && handler.Logger != nil {
			handler.Logger.Warnf(
				"Upload failed stage=%s method=%s server=%s dataType=%s expectedGameUserId=%s parsedGameUserId=%s parsedGameUserIdType=%s err=%v",
				stage,
				uploadCtx.UploadMethod,
				uploadCtx.Server,
				uploadCtx.DataType,
				uploadCtx.expectedGameUserIDString(),
				uploadCtx.parsedGameUserIDString(),
				uploadCtx.ParsedGameUserIDType,
				err,
			)
		}
		writeUploadAudit(false, buildUploadAuditErrorMessage(err, result))
		return result, err
	}

	exists, belongs, settings, allowCNMySekai, userBanned, banReason, err := ParseGameAccountSetting(ctx, helper.DBManager.DB, string(uploadCtx.Server), uploadCtx.expectedGameUserIDString(), uploadCtx.UploadMethod, userID)
	if err != nil {
		return fail(uploadStageAccountPolicy, nil, err)
	}
	uploadCtx.Settings = settings
	if userBanned != nil && *userBanned {
		banMessage := "account owner is banned"
		if banReason != nil && *banReason != "" {
			banMessage = "account owner is banned: " + *banReason
		}
		err = fmt.Errorf("%w: %s", errUploadOwnerBanned, banMessage)
		return fail(uploadStageAccountPolicy, nil, err)
	}
	if err := validateGameAccountBelonging(belongs); err != nil {
		return fail(uploadStageAccountPolicy, nil, err)
	}
	uploadCtx.AllowPublicAPI = determinePublicAPIPermission(exists, uploadCtx.DataType, uploadCtx.Settings)
	if err := validateCNMysekaiAccess(uploadCtx.DataType, uploadCtx.Server, allowCNMySekai); err != nil {
		return fail(uploadStageAccountPolicy, nil, err)
	}

	unpackedMap, result, err := handler.DecodeUploadData(data, uploadCtx.Server)
	if err != nil {
		return fail(uploadStageDecodePayload, result, err)
	}
	parsedUserID, err := handler.ExtractGameUserIDForExpected(unpackedMap, &uploadCtx.ExpectedGameUserID)
	uploadCtx.ParsedGameUserID = parsedUserID.Value
	uploadCtx.ParsedGameUserIDType = parsedUserID.RawType
	if err != nil {
		return fail(uploadStageValidateIdentity, nil, err)
	}
	processedData, err := handler.PreHandleData(unpackedMap, &uploadCtx.ExpectedGameUserID, uploadCtx.ParsedGameUserID, uploadCtx.Server, uploadCtx.DataType)
	if err != nil {
		return fail(uploadStagePreprocess, nil, err)
	}
	if err := handler.PersistUploadData(ctx, processedData, uploadCtx.Server, uploadCtx.DataType, &uploadCtx.ExpectedGameUserID); err != nil {
		return fail(uploadStagePersist, nil, err)
	}
	result = &harukiUtils.HandleDataResult{UserID: &uploadCtx.ExpectedGameUserID}
	if err := validateUploadResult(result); err != nil {
		return fail(uploadStageValidateResult, result, err)
	}
	writeUploadAudit(true, nil)
	if err = helper.DBManager.Redis.ClearCache(ctx, string(uploadCtx.DataType), string(uploadCtx.Server), uploadCtx.ExpectedGameUserID); err != nil {
		handler.Logger.Warnf("Failed to clear redis cache: %v", err)
	}
	handler.RunUploadFanout(data, processedData, uploadCtx.Server, uploadCtx.DataType, &uploadCtx.ExpectedGameUserID, uploadCtx.Settings, uploadCtx.AllowPublicAPI)
	return result, nil
}

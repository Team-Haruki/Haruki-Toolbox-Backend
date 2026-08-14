package upload

import (
	"context"
	"fmt"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiBackground "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/background"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"strings"
	"time"
)

var uploadAuditSemaphore = make(chan struct{}, 64)

func buildUploadAuditErrorMessage(err error, result *harukiUtils.HandleDataResult) *string {
	if result != nil {
		parts := make([]string, 0, 2)
		if result.Status != nil {
			parts = append(parts, fmt.Sprintf("status=%d", *result.Status))
		}
		if result.ErrorMessage != nil {
			trimmed := strings.TrimSpace(*result.ErrorMessage)
			if trimmed != "" {
				parts = append(parts, trimmed)
			}
		}
		if len(parts) > 0 {
			message := strings.Join(parts, " ")
			return &message
		}
	}
	if err == nil {
		return nil
	}
	trimmed := strings.TrimSpace(err.Error())
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func dispatchUploadAuditLog(
	helper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	logger *harukiLogger.Logger,
	backgroundTasks harukiBackground.Runner,
	uploadCtx *uploadContext,
	success bool,
	errorMessage *string,
) {
	dispatchUploadAuditLogWithSemaphore(uploadAuditSemaphore, helper, logger, backgroundTasks, uploadCtx, success, errorMessage)
}

func dispatchUploadAuditLogWithSemaphore(
	semaphore chan struct{},
	helper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	logger *harukiLogger.Logger,
	backgroundTasks harukiBackground.Runner,
	uploadCtx *uploadContext,
	success bool,
	errorMessage *string,
) {
	select {
	case semaphore <- struct{}{}:
		accepted := startBackgroundTask(backgroundTasks, logger, "upload-audit", func() {
			defer func() { <-semaphore }()
			persistUploadAuditLog(helper, logger, uploadCtx, success, errorMessage)
		})
		if !accepted {
			// The task never took ownership of the reserved slot.
			<-semaphore
			// Preserve the audit record and the historical caller-side
			// backpressure behavior even if lifecycle admission is rejected.
			persistUploadAuditLog(helper, logger, uploadCtx, success, errorMessage)
		}
	default:
		// Preserve the historical bounded/backpressure behavior: at most 64
		// detached audit goroutines; overflow persists in the request/parent task.
		// For an iOS parent using InlineRunner, both branches remain inside that
		// already tracked parent lifetime.
		persistUploadAuditLog(helper, logger, uploadCtx, success, errorMessage)
	}
}

func persistUploadAuditLog(
	helper *harukiAPIHelper.HarukiToolboxRouterHelpers,
	logger *harukiLogger.Logger,
	uploadCtx *uploadContext,
	success bool,
	errorMessage *string,
) {
	if helper == nil || helper.DBManager == nil || helper.DBManager.DB == nil {
		if logger != nil {
			logger.Warnf("Skip upload audit log because DB helper is unavailable")
		}
		return
	}

	logCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	create := helper.DBManager.DB.UploadLog.Create().
		SetServer(string(uploadCtx.Server)).
		SetGameUserID(uploadCtx.expectedGameUserIDString()).
		SetToolboxUserID(uploadCtx.ToolboxUserID).
		SetDataType(string(uploadCtx.DataType)).
		SetUploadMethod(string(uploadCtx.UploadMethod)).
		SetSuccess(success).
		SetUploadTime(time.Now())
	if errorMessage != nil {
		create.SetErrorMessage(*errorMessage)
	}
	_, logErr := create.Save(logCtx)
	if logErr != nil {
		if logger != nil {
			logger.Warnf("Failed to create upload log: %v", logErr)
		}
	}

	targetType := "game_account"
	targetID := fmt.Sprintf("%s:%d", uploadCtx.Server, uploadCtx.ExpectedGameUserID)
	action := "user.upload." + strings.ToLower(string(uploadCtx.UploadMethod))
	actorType := harukiAPIHelper.SystemLogActorTypeSystem
	var actorUserID *string
	if strings.TrimSpace(uploadCtx.ToolboxUserID) != "" {
		actorType = harukiAPIHelper.SystemLogActorTypeUser
		userIDCopy := uploadCtx.ToolboxUserID
		actorUserID = &userIDCopy
	}

	systemLogErr := harukiAPIHelper.WriteSystemLog(logCtx, helper, harukiAPIHelper.SystemLogEntry{
		ActorUserID: actorUserID,
		ActorType:   actorType,
		Action:      action,
		TargetType:  &targetType,
		TargetID:    &targetID,
		Result: map[bool]string{
			true:  harukiAPIHelper.SystemLogResultSuccess,
			false: harukiAPIHelper.SystemLogResultFailure,
		}[success],
		Metadata: map[string]any{
			"server":               string(uploadCtx.Server),
			"gameUserId":           uploadCtx.expectedGameUserIDString(),
			"expectedGameUserId":   uploadCtx.expectedGameUserIDString(),
			"parsedGameUserId":     uploadCtx.parsedGameUserIDString(),
			"parsedGameUserIdType": uploadCtx.ParsedGameUserIDType,
			"dataType":             string(uploadCtx.DataType),
			"uploadMethod":         string(uploadCtx.UploadMethod),
			"failureStage":         uploadCtx.FailureStage,
			"errorMessage": func() string {
				if errorMessage == nil {
					return ""
				}
				return *errorMessage
			}(),
		},
	})
	if systemLogErr != nil && logger != nil {
		logger.Warnf("Failed to create system log: %v", systemLogErr)
	}
}

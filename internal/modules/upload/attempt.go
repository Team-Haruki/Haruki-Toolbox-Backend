package upload

import (
	"context"
	"errors"
	"strings"
	"time"

	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	platform "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/upload"
	utils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/gameaccountbinding"
	"github.com/google/uuid"
)

func newAttempt(method utils.UploadMethod, size int) *platform.Attempt {
	a := &platform.Attempt{RequestID: uuid.NewString(), ReceivedAt: time.Now().UTC(), RequestBytes: int64(size)}
	if method == utils.UploadMethodInherit {
		a.Client.Platform = "not_applicable"
	}
	return a
}

func classifyAttemptFailure(a *platform.Attempt, stage string, err error) {
	a.FailureStage = stage
	a.ErrorCode = "invalid_upload_payload"
	a.HTTPStatus = 400
	a.Retryable = false
	switch {
	case errors.Is(err, errUploadOwnershipMismatch), errors.Is(err, errUploadOwnerBanned), errors.Is(err, errUploadCNMysekaiDenied):
		a.ErrorCode = "upload_not_allowed"
		a.HTTPStatus = 403
	case stage == uploadStageAccountPolicy:
		a.ErrorCode = "temporarily_unavailable"
		a.HTTPStatus = 503
		a.Retryable = true
	case stage == uploadStagePersist || stage == uploadStageValidateResult:
		// A write may have committed before its error reached us.
		a.ErrorCode = "internal_error"
		a.HTTPStatus = 500
	}
}

func resolveAttemptOwner(ctx context.Context, helper *api.HarukiToolboxRouterHelpers, uc *uploadContext) error {
	if !uc.Attempt.IdentityVerified {
		uc.ToolboxUserID = ""
		return nil
	}
	binding, err := helper.DBManager.DB.GameAccountBinding.Query().Where(
		gameaccountbinding.ServerEQ(string(uc.Server)), gameaccountbinding.GameUserIDEQ(uc.expectedGameUserIDString()),
	).WithUser().Only(ctx)
	if postgresql.IsNotFound(err) {
		uc.ToolboxUserID = ""
		return nil
	}
	if err != nil {
		return err
	}
	uc.ToolboxUserID = ""
	if binding.Edges.User != nil {
		uc.ToolboxUserID = strings.Clone(binding.Edges.User.ID)
	}
	return nil
}

func finishAttempt(uc *uploadContext, success bool) {
	if uc.Attempt == nil {
		return
	}
	uc.Attempt.DurationMS = max(0, time.Since(uc.Attempt.ReceivedAt).Milliseconds())
	if success {
		uc.Attempt.HTTPStatus = 200
		uc.Attempt.ErrorCode = ""
		uc.Attempt.FailureStage = ""
		uc.Attempt.Retryable = false
	}
}

func uploadAttemptRequestID(uc *uploadContext) *string {
	if uc.Attempt == nil {
		return nil
	}
	id := uc.Attempt.RequestID
	return &id
}

func applyAttemptLogFields(create *postgresql.UploadLogCreate, uc *uploadContext) {
	a := uc.Attempt
	if a == nil {
		return
	}
	create.SetReceivedAt(a.ReceivedAt).SetRequestID(a.RequestID).SetRequestBytes(a.RequestBytes).
		SetProcessingDurationMs(a.DurationMS).SetIdentityVerified(a.IdentityVerified).
		SetClaimedGameUserID(uc.expectedGameUserIDString())
	m := a.Client
	setters := []struct {
		value string
		set   func(string) *postgresql.UploadLogCreate
	}{
		{m.Name, create.SetClientName}, {m.Version, create.SetClientVersion}, {m.Channel, create.SetClientChannel},
		{m.Format, create.SetClientMetadataFormat}, {m.Protocol, create.SetProtocolVersion}, {m.Platform, create.SetPlatform},
		{m.OSVersion, create.SetOsVersion}, {m.OSBuild, create.SetOsBuild}, {m.OSArch, create.SetOsArch}, {m.AppArch, create.SetAppArch},
		{m.OAuthClientID, create.SetOauthClientID}, {a.FailureStage, create.SetFailureStage}, {a.ErrorCode, create.SetErrorCode},
	}
	for _, s := range setters {
		if s.value != "" {
			s.set(s.value)
		}
	}
}

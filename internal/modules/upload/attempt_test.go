package upload

import (
	"context"
	"errors"
	"testing"

	platform "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/upload"
)

func TestAttemptFailureRetrySafety(t *testing.T) {
	for _, tc := range []struct {
		stage  string
		err    error
		code   string
		status int
		retry  bool
	}{
		{uploadStageAccountPolicy, errUploadOwnerBanned, "upload_not_allowed", 403, false},
		{uploadStageAccountPolicy, errUploadOwnershipMismatch, "upload_not_allowed", 403, false},
		{uploadStageAccountPolicy, context.DeadlineExceeded, "temporarily_unavailable", 503, true},
		{uploadStageDecodePayload, errors.New("malformed"), "invalid_upload_payload", 400, false},
		{uploadStagePersist, context.DeadlineExceeded, "internal_error", 500, false},
		{uploadStageValidateResult, errors.New("unexpected result"), "internal_error", 500, false},
	} {
		var a platform.Attempt
		classifyAttemptFailure(&a, tc.stage, tc.err)
		if a.ErrorCode != tc.code || a.HTTPStatus != tc.status || a.Retryable != tc.retry {
			t.Fatalf("%s: %+v", tc.stage, a)
		}
	}
}

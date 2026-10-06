package upload

import (
	"context"
	"errors"
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	utils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/background"
	database "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"testing"
	"time"

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

func TestAttemptOwnerAndCompletion(t *testing.T) {
	client := enttest.Open(t, "sqlite3", uniqueUploadAuditSQLiteDSN(t, "attempt-owner"))
	defer client.Close()
	ctx := context.Background()
	user, err := client.User.Create().SetID("owner").SetName("owner").SetEmail("owner@example.com").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GameAccountBinding.Create().SetServer("jp").SetGameUserID("123").SetVerified(true).SetUser(user).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	helper := &api.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: client}}
	uc := &uploadContext{Server: "jp", ExpectedGameUserID: 123, ToolboxUserID: "forged", Attempt: &platform.Attempt{}}
	if err := resolveAttemptOwner(ctx, helper, uc); err != nil || uc.ToolboxUserID != "" {
		t.Fatalf("unverified identity attributed: %v", err)
	}
	uc.Attempt.IdentityVerified = true
	if err := resolveAttemptOwner(ctx, helper, uc); err != nil || uc.ToolboxUserID != "owner" {
		t.Fatalf("verified owner not resolved: %v", err)
	}
	uc.ExpectedGameUserID = 124
	if err := resolveAttemptOwner(ctx, helper, uc); err != nil || uc.ToolboxUserID != "" {
		t.Fatalf("missing binding attributed: %v", err)
	}
	uc.Attempt.ReceivedAt = time.Now().Add(-time.Second)
	uc.Attempt.ErrorCode = "stale"
	uc.Attempt.Retryable = true
	uc.Attempt.FailureStage = "stale"
	finishAttempt(uc, true)
	if uc.Attempt.HTTPStatus != 200 || uc.Attempt.ErrorCode != "" || uc.Attempt.Retryable || uc.Attempt.FailureStage != "" || uc.Attempt.DurationMS < 1000 {
		t.Fatalf("invalid completion: %+v", uc.Attempt)
	}
}

func TestIdentifiedUploadMethodsShareWritePolicy(t *testing.T) {
	client := enttest.Open(t, "sqlite3", uniqueUploadAuditSQLiteDSN(t, "shared-write-policy"))
	defer client.Close()
	ctx := context.Background()
	for _, id := range []string{"owner", "actor"} {
		if _, err := client.User.Create().SetID(id).SetName(id).SetEmail(id + "@example.com").Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.GameAccountBinding.Create().SetServer("jp").SetGameUserID("123").SetVerified(true).SetUserID("owner").Save(ctx); err != nil {
		t.Fatal(err)
	}
	helper := &api.HarukiToolboxRouterHelpers{DBManager: &database.HarukiToolboxDBManager{DB: client}}
	actor := "actor"
	game := int64(123)
	for _, permission := range []string{"read", "write"} {
		if _, err := client.UpsertGameAccountDataGrant(ctx, "owner", actor, "jp", "123", "suite", time.Now().Add(time.Hour), []string{permission}); err != nil {
			t.Fatal(err)
		}
		for _, method := range []utils.UploadMethod{utils.UploadMethodManual, utils.UploadMethodIOSScript, utils.UploadMethodOAuth2, utils.UploadMethodHarukiProxy} {
			a := newAttempt(method, 3)
			d := testUploadDependencies()
			d.BackgroundTasks = background.InlineRunner{}
			_, err := HandleUpload(ctx, []byte("bad"), "jp", "suite", &game, &actor, helper, d, method, a)
			if err == nil {
				t.Fatal("invalid game payload accepted")
			}
			if permission == "read" && !errors.Is(err, errUploadOwnershipMismatch) {
				t.Fatalf("%s read grant accepted: %v", method, err)
			}
			if permission == "write" && (errors.Is(err, errUploadOwnershipMismatch) || a.FailureStage != uploadStageDecodePayload) {
				t.Fatalf("%s write grant rejected: %s %v", method, a.FailureStage, err)
			}
		}
	}
}

package upload

import (
	"context"
	"errors"
	"testing"
	"time"

	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"haruki-suite/utils/database"
	"haruki-suite/utils/database/postgresql"
	"haruki-suite/utils/database/postgresql/enttest"
	"haruki-suite/utils/database/postgresql/systemlog"
	"haruki-suite/utils/database/postgresql/uploadlog"

	_ "github.com/mattn/go-sqlite3"
)

func TestHandleUploadWritesFailureAuditLogForCNMysekaiPrecheck(t *testing.T) {
	t.Parallel()

	client := enttest.Open(t, "sqlite3", "file:upload-audit-test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() {
		_ = client.Close()
	})

	ctx := context.Background()
	user, err := client.User.Create().
		SetID("1000000001").
		SetName("tester").
		SetEmail("tester@example.com").
		SetAllowCnMysekai(false).
		Save(ctx)
	if err != nil {
		t.Fatalf("create user returned error: %v", err)
	}

	if _, err := client.GameAccountBinding.Create().
		SetServer("cn").
		SetGameUserID("7486311609544252170").
		SetVerified(true).
		SetUser(user).
		Save(ctx); err != nil {
		t.Fatalf("create binding returned error: %v", err)
	}

	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{
			DB: client,
		},
	}

	gameUserID := int64(7486311609544252170)
	_, err = HandleUpload(
		ctx,
		[]byte("{}"),
		harukiUtils.SupportedDataUploadServerCN,
		harukiUtils.UploadDataTypeMysekai,
		&gameUserID,
		nil,
		helper,
		harukiUtils.UploadMethodIOSProxy,
	)
	if !errors.Is(err, errUploadCNMysekaiDenied) {
		t.Fatalf("HandleUpload error = %v, want errUploadCNMysekaiDenied", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		row, queryErr := client.UploadLog.Query().
			Where(
				uploadlog.ServerEQ("cn"),
				uploadlog.GameUserIDEQ("7486311609544252170"),
				uploadlog.DataTypeEQ(string(harukiUtils.UploadDataTypeMysekai)),
				uploadlog.UploadMethodEQ(string(harukiUtils.UploadMethodIOSProxy)),
			).
			Only(ctx)
		if queryErr == nil {
			if row.Success {
				t.Fatalf("upload log success = true, want false")
			}
			if row.ErrorMessage == nil || *row.ErrorMessage != errUploadCNMysekaiDenied.Error() {
				t.Fatalf("upload log error_message = %v, want %q", row.ErrorMessage, errUploadCNMysekaiDenied.Error())
			}
			syslog, syslogErr := client.SystemLog.Query().
				Where(
					systemlog.ActionEQ("user.upload."+string(harukiUtils.UploadMethodIOSProxy)),
					systemlog.TargetIDEQ("cn:7486311609544252170"),
					systemlog.ResultEQ(systemlog.ResultFailure),
				).
				Only(ctx)
			if syslogErr != nil {
				t.Fatalf("query system log returned error: %v", syslogErr)
			}
			if syslog.Metadata["failureStage"] != uploadStageAccountPolicy {
				t.Fatalf("system log failureStage = %v, want %q", syslog.Metadata["failureStage"], uploadStageAccountPolicy)
			}
			if syslog.Metadata["expectedGameUserId"] != "7486311609544252170" {
				t.Fatalf("system log expectedGameUserId = %v", syslog.Metadata["expectedGameUserId"])
			}
			if syslog.Metadata["uploadMethod"] != string(harukiUtils.UploadMethodIOSProxy) {
				t.Fatalf("system log uploadMethod = %v", syslog.Metadata["uploadMethod"])
			}
			return
		}
		if !postgresql.IsNotFound(queryErr) {
			t.Fatalf("query upload log returned error: %v", queryErr)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for upload log to be written")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

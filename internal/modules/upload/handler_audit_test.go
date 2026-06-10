package upload

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/systemlog"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/uploadlog"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/sekai"

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

func TestRecordInheritRetrievalFailureWritesUploadLog(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:inherit-retrieval-audit-test?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() {
		_ = client.Close()
	})

	helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{
		DBManager: &database.HarukiToolboxDBManager{
			DB: client,
		},
	}
	gameUserID := int64(164337024457871363)
	err := harukiSekai.NewDataRetrievalError(
		string(harukiUtils.UploadDataTypeSuite),
		"api_call",
		"failed to call suite API",
		harukiSekai.NewAPIError("/suite/user/164337024457871363", "GET", 426, "non-200 response", nil),
	)

	recordInheritRetrievalFailure(
		helper,
		harukiUtils.SupportedDataUploadServerEN,
		harukiUtils.UploadDataTypeSuite,
		&harukiUtils.SekaiInheritDataRetrieverResponse{UserID: gameUserID},
		err,
	)

	ctx := context.Background()
	deadline := time.Now().Add(2 * time.Second)
	for {
		row, queryErr := client.UploadLog.Query().
			Where(
				uploadlog.ServerEQ("en"),
				uploadlog.GameUserIDEQ("164337024457871363"),
				uploadlog.DataTypeEQ(string(harukiUtils.UploadDataTypeSuite)),
				uploadlog.UploadMethodEQ(string(harukiUtils.UploadMethodInherit)),
			).
			Only(ctx)
		if queryErr == nil {
			if row.Success {
				t.Fatalf("upload log success = true, want false")
			}
			if row.ErrorMessage == nil || !strings.Contains(*row.ErrorMessage, "status 426") {
				t.Fatalf("upload log error_message = %v, want status 426 detail", row.ErrorMessage)
			}
			syslog, syslogErr := client.SystemLog.Query().
				Where(
					systemlog.ActionEQ("user.upload."+string(harukiUtils.UploadMethodInherit)),
					systemlog.TargetIDEQ("en:164337024457871363"),
					systemlog.ResultEQ(systemlog.ResultFailure),
				).
				Only(ctx)
			if syslogErr != nil {
				t.Fatalf("query system log returned error: %v", syslogErr)
			}
			if syslog.Metadata["failureStage"] != "retrieve_suite" {
				t.Fatalf("system log failureStage = %v, want retrieve_suite", syslog.Metadata["failureStage"])
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

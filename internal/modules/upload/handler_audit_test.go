package upload

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/systemlog"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/uploadlog"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/sekai"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"

	_ "github.com/mattn/go-sqlite3"
)

type queuedUploadRunner struct {
	mu     sync.Mutex
	accept bool
	calls  int
	tasks  []func()
}

func (r *queuedUploadRunner) Go(_ string, task func()) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.accept {
		r.tasks = append(r.tasks, task)
	}
	return r.accept
}

func (r *queuedUploadRunner) snapshot() (int, []func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls, append([]func(){}, r.tasks...)
}

func TestDispatchUploadAuditKeepsBoundedAsyncSlotsUnderConcurrency(t *testing.T) {
	t.Parallel()

	const capacity = 4
	semaphore := make(chan struct{}, capacity)
	runner := &queuedUploadRunner{accept: true}
	logger := harukiLogger.NewLogger("audit-test", "DEBUG", &bytes.Buffer{})
	var callers sync.WaitGroup
	for range 64 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			dispatchUploadAuditLogWithSemaphore(semaphore, nil, logger, runner, nil, false, nil)
		}()
	}
	callers.Wait()

	calls, tasks := runner.snapshot()
	if calls != capacity {
		t.Fatalf("async runner calls = %d, want semaphore capacity %d", calls, capacity)
	}
	if len(tasks) != capacity || len(semaphore) != capacity {
		t.Fatalf("queued tasks/slots = %d/%d, want %d/%d", len(tasks), len(semaphore), capacity, capacity)
	}
	for _, task := range tasks {
		task()
	}
	if len(semaphore) != 0 {
		t.Fatalf("semaphore slots after tasks = %d, want 0", len(semaphore))
	}
}

func TestDispatchUploadAuditRejectionLogsAndReleasesSlot(t *testing.T) {
	t.Parallel()

	semaphore := make(chan struct{}, 1)
	runner := &queuedUploadRunner{accept: false}
	var output bytes.Buffer
	logger := harukiLogger.NewLogger("audit-test", "DEBUG", &output)
	dispatchUploadAuditLogWithSemaphore(semaphore, nil, logger, runner, nil, false, nil)

	calls, tasks := runner.snapshot()
	if calls != 1 || len(tasks) != 0 {
		t.Fatalf("runner calls/tasks = %d/%d, want 1/0", calls, len(tasks))
	}
	if len(semaphore) != 0 {
		t.Fatalf("semaphore slots after rejection = %d, want 0", len(semaphore))
	}
	if !strings.Contains(output.String(), "rejected") {
		t.Fatalf("rejection log = %q, want warning", output.String())
	}
}

func TestHandleUploadWritesFailureAuditLogForCNMysekaiPrecheck(t *testing.T) {
	t.Parallel()

	client := enttest.Open(t, "sqlite3", uniqueUploadAuditSQLiteDSN(t, "upload-audit-test"))
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
		testUploadDependencies(),
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
				uploadlog.ClaimedGameUserIDEQ("7486311609544252170"),
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
			if row.GameUserID != "" || row.ToolboxUserID != "" || row.IdentityVerified == nil || *row.IdentityVerified {
				t.Fatal("unverified claim attributed to account")
			}
			if row.FailureStage == nil || *row.FailureStage != uploadStageAccountPolicy {
				t.Fatal("missing failure stage")
			}
			count, err := client.SystemLog.Query().Count(ctx)
			if err != nil || count != 0 {
				t.Fatalf("unverified account leaked into system log: %d %v", count, err)
			}
			return
		}
		if queryErr != nil && !postgresql.IsNotFound(queryErr) && !strings.Contains(strings.ToLower(queryErr.Error()), "database table is locked") {
			t.Fatalf("query upload log returned error: %v", queryErr)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for upload log to be written")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRecordInheritRetrievalFailureWritesUploadLog(t *testing.T) {
	client := enttest.Open(t, "sqlite3", uniqueUploadAuditSQLiteDSN(t, "inherit-retrieval-audit-test"))
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
		testUploadDependencies(),
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
				// The system log is written after the upload log by the same
				// async audit goroutine, so it may not be visible yet (or the
				// SQLite table may be mid-write) — keep polling until deadline.
				if postgresql.IsNotFound(syslogErr) || strings.Contains(strings.ToLower(syslogErr.Error()), "database table is locked") {
					if time.Now().After(deadline) {
						t.Fatalf("timed out waiting for system log to be written")
					}
					time.Sleep(20 * time.Millisecond)
					continue
				}
				t.Fatalf("query system log returned error: %v", syslogErr)
			}
			if syslog.Metadata["failureStage"] != "retrieve_suite" {
				t.Fatalf("system log failureStage = %v, want retrieve_suite", syslog.Metadata["failureStage"])
			}
			return
		}
		if queryErr != nil && !postgresql.IsNotFound(queryErr) && !strings.Contains(strings.ToLower(queryErr.Error()), "database table is locked") {
			t.Fatalf("query upload log returned error: %v", queryErr)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for upload log to be written")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func uniqueUploadAuditSQLiteDSN(t *testing.T, name string) string {
	t.Helper()
	return fmt.Sprintf("file:%s-%s?mode=memory&cache=shared&_fk=1", name, strings.ReplaceAll(t.Name(), "/", "-"))
}

// The upload audit error message is stored and shown to admins, so it is
// redacted whichever branch builds it.
func TestBuildUploadAuditErrorMessageRedacts(t *testing.T) {
	t.Parallel()

	const inheritID = "FAKEinheritID0008"
	errMsg := buildUploadAuditErrorMessage(errors.New("sekai API error: POST /inherit/user/"+inheritID+"?isExecuteInherit=False returned status 403"), nil)
	if errMsg == nil || strings.Contains(*errMsg, inheritID) || !strings.Contains(*errMsg, "/inherit/user/<redacted>") {
		t.Fatalf("error branch = %v", errMsg)
	}

	status := 500
	detail := `upstream said {"inherit_password":"FAKEinheritPW0008"}`
	resultMsg := buildUploadAuditErrorMessage(nil, &harukiUtils.HandleDataResult{Status: &status, ErrorMessage: &detail})
	if resultMsg == nil || strings.Contains(*resultMsg, "FAKEinheritPW0008") || !strings.HasPrefix(*resultMsg, "status=500 ") {
		t.Fatalf("result branch = %v", resultMsg)
	}

	if got := buildUploadAuditErrorMessage(nil, nil); got != nil {
		t.Fatalf("no error = %q, want nil", *got)
	}
}

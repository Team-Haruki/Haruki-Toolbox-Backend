package handler

import (
	"bytes"
	"sync"
	"testing"
	"time"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiSchema "github.com/Team-Haruki/Haruki-Toolbox-Backend/ent/toolbox/schema"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	apiHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/sekai"
)

type capturedHandlerRunner struct {
	mu     sync.Mutex
	accept bool
	names  []string
	tasks  []func()
}

func (r *capturedHandlerRunner) Go(name string, task func()) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.names = append(r.names, name)
	if r.accept {
		r.tasks = append(r.tasks, task)
	}
	return r.accept
}

func TestRunUploadFanoutSubmitsOneTrackedParentTask(t *testing.T) {
	t.Parallel()

	runner := &capturedHandlerRunner{accept: true}
	handler := &DataHandler{
		BackgroundTasks: runner,
		Logger:          testLogger(),
	}
	userID := int64(123)
	handler.RunUploadFanout(
		[]byte("raw"),
		map[string]any{},
		utils.SupportedDataUploadServerJP,
		utils.UploadDataTypeSuite,
		&userID,
		apiHelper.HarukiToolboxGameAccountPrivacySettings{},
		false,
	)

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.names) != 1 || runner.names[0] != "upload-fanout" {
		t.Fatalf("submitted task names = %v, want [upload-fanout]", runner.names)
	}
	if len(runner.tasks) != 1 {
		t.Fatalf("captured task count = %d, want 1", len(runner.tasks))
	}
}

func TestRunUploadFanoutLogsRejectedTrackedTask(t *testing.T) {
	t.Parallel()

	runner := &capturedHandlerRunner{accept: false}
	var output bytes.Buffer
	handler := &DataHandler{
		BackgroundTasks: runner,
		Logger:          harukiLogger.NewLogger("fanout-test", "DEBUG", &output),
	}
	userID := int64(123)
	handler.RunUploadFanout(
		[]byte("raw"),
		map[string]any{},
		utils.SupportedDataUploadServerJP,
		utils.UploadDataTypeSuite,
		&userID,
		apiHelper.HarukiToolboxGameAccountPrivacySettings{},
		false,
	)
	if !bytes.Contains(output.Bytes(), []byte("rejected")) {
		t.Fatalf("rejection log = %q, want warning", output.String())
	}
}

func TestRunDataSyncerWaitsForConcurrentSends(t *testing.T) {
	t.Parallel()

	cfg := harukiConfig.ThirdPartyDataProviderConfig{
		EndpointSakura: "https://sakura.example/upload",
		EndpointResona: "https://resona.example/upload",
	}
	settings := apiHelper.HarukiToolboxGameAccountPrivacySettings{
		Suite: &harukiSchema.SuiteDataPrivacySettings{
			AllowSakura: true,
			AllowResona: true,
		},
	}
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		runDataSyncer(
			cfg,
			123,
			utils.SupportedDataUploadServerJP,
			utils.UploadDataTypeSuite,
			[]byte("raw"),
			settings,
			sekai.ServerCryptor{},
			nil,
			func(string, int64, utils.SupportedDataUploadServer, utils.UploadDataType, []byte, string, map[string]string) {
				started <- struct{}{}
				<-release
			},
		)
		close(done)
	}()

	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for concurrent send to start")
		}
	}
	select {
	case <-done:
		t.Fatal("runDataSyncer returned before child sends finished")
	default:
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runDataSyncer did not return after child sends finished")
	}
}

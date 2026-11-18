package upload

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cfg "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	schema "github.com/Team-Haruki/Haruki-Toolbox-Backend/ent/toolbox/schema"
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/background"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/database/postgresql/enttest"

	_ "github.com/mattn/go-sqlite3"
)

func uploadDeliveryBinding(t *testing.T, h *DataHandler) api.HarukiToolboxGameAccountPrivacySettings {
	t.Helper()
	db := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.User.Create().SetID("owner").SetName("Owner").SetEmail("owner@example.com").Save(t.Context()); err != nil {
		t.Fatal(err)
	}
	suite := &schema.SuiteDataPrivacySettings{AllowSakura: true, AllowPublicApi: true}
	binding, err := db.GameAccountBinding.Create().SetUserID("owner").SetGameUserID("123").SetServer("jp").SetVerified(true).SetSuite(suite).Save(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	h.DBManager = &database.HarukiToolboxDBManager{DB: db}
	return api.HarukiToolboxGameAccountPrivacySettings{BindingID: binding.ID, OwnerID: "owner", Suite: suite}
}

func TestFanoutSlowHTTPPreservesCopiedPayloadAndDrains(t *testing.T) {
	entered := make(chan struct{}, 6)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var deliveries atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) != (1<<20) || !bytes.Equal(body, bytes.Repeat([]byte{0x41}, 1<<20)) {
			t.Error("copied payload changed or truncated")
		}
		deliveries.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	defer unblock()
	group := background.NewTaskGroup(func(name string, recovered any) { t.Errorf("task %s panicked: %v", name, recovered) })
	h := &DataHandler{BackgroundTasks: group, Logger: testLogger(), DataSync: NewDataSyncConfig(cfg.ThirdPartyDataProviderConfig{EndpointSakura: server.URL})}
	settings := uploadDeliveryBinding(t, h)
	var callers sync.WaitGroup
	for range 6 {
		callers.Go(func() {
			raw := bytes.Repeat([]byte{0x41}, 1<<20)
			id := int64(123)
			h.RunUploadFanout(raw, nil, utils.SupportedDataUploadServerJP, utils.UploadDataTypeSuite, &id, settings, false)
			// Mimic the HTTP body being reused immediately after the handler returns.
			clear(raw)
		})
	}
	// Ensure test failures release senders, then finish readers before restoring config.
	defer func() {
		unblock()
		callers.Wait()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = group.Shutdown(ctx)
	}()
	for range 4 {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("first four deliveries not started")
		}
	}
	waitFanoutState(t, uploadFanoutLimit, 2)
	if st := uploadFanoutLimit.snapshot(); st.Active != 4 || st.ActiveInputBytes != 4<<20 {
		t.Fatalf("unexpected admitted work: %+v", st)
	}
	unblock()
	callers.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := group.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if deliveries.Load() != 6 {
		t.Fatalf("delivered %d of 6", deliveries.Load())
	}
	if st := uploadFanoutLimit.snapshot(); st.Active != 0 || st.Waiting != 0 || st.ActiveInputBytes != 0 {
		t.Fatalf("capacity leaked after drain: %+v", st)
	}
}

func TestFanoutRechecksSharingAfterProviderLookup(t *testing.T) {
	var checked, delivered atomic.Bool
	const gameID = int64(123)
	runner := &capturedHandlerRunner{accept: true}
	h := &DataHandler{BackgroundTasks: runner, Logger: testLogger()}
	settings := uploadDeliveryBinding(t, h)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/check" {
			delivered.Store(true)
			return
		}
		checked.Store(true)
		if err := h.DBManager.DB.GameAccountBinding.UpdateOneID(settings.BindingID).SetSuite(&schema.SuiteDataPrivacySettings{}).Exec(r.Context()); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)
	h.DataSync = NewDataSyncConfig(cfg.ThirdPartyDataProviderConfig{
		EndpointSakura: server.URL + "/deliver", CheckEnabledSakura: true, CheckURLSakura: server.URL + "/check",
	})
	id := gameID
	h.RunUploadFanout([]byte("raw"), nil, utils.SupportedDataUploadServerJP, utils.UploadDataTypeSuite, &id, settings, true)
	if len(runner.tasks) != 1 {
		t.Fatal("fanout not queued")
	}
	runner.tasks[0]()
	if !checked.Load() || delivered.Load() {
		t.Fatalf("provider lookup = %t, delivery = %t", checked.Load(), delivered.Load())
	}
	if h.currentUploadPrivacy(t.Context(), settings, gameID, utils.SupportedDataUploadServerJP).AllowsPublicAPI(utils.UploadDataTypeSuite) {
		t.Fatal("the earlier public opt-in survived the privacy update")
	}
}

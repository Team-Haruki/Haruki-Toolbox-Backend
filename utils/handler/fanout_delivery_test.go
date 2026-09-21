package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cfg "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	schema "github.com/Team-Haruki/Haruki-Toolbox-Backend/ent/toolbox/schema"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	api "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/background"
)

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
	settings := api.HarukiToolboxGameAccountPrivacySettings{Suite: &schema.SuiteDataPrivacySettings{AllowSakura: true}}
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

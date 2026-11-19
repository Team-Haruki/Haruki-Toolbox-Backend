package upload

import (
	"context"
	"io"
	"reflect"
	"testing"
	"time"

	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/api"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/runtimeconfig"
	harukiDataHandler "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/upload"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/game/sekai"
	harukiHttp "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/http"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
)

type uploadAuthorizerStub struct{}

type blockedWebhookConfigStore struct {
	runtimeconfig.Store
	entered chan struct{}
}

func (s blockedWebhookConfigStore) Load(ctx context.Context) (runtimeconfig.Snapshot, bool, error) {
	s.entered <- struct{}{}
	<-ctx.Done()
	return runtimeconfig.Snapshot{}, false, ctx.Err()
}

func TestUploadWebhookRetainsPolicyWhenRefreshIsBusy(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		store := blockedWebhookConfigStore{entered: make(chan struct{}, 16)}
		service := runtimeconfig.New(runtimeconfig.Snapshot{WebhookEnabled: &enabled}, store)
		helper := &harukiAPIHelper.HarukiToolboxRouterHelpers{RuntimeConfig: service}
		handler := newUploadDataHandler(helper, testUploadDependencies())
		ctx, cancel := context.WithCancel(t.Context())
		finished := make(chan struct{}, cap(store.entered))
		for range cap(store.entered) {
			go func() {
				_, _ = service.Current(ctx)
				finished <- struct{}{}
			}()
		}
		for range cap(store.entered) {
			select {
			case <-store.entered:
			case <-time.After(time.Second):
				cancel()
				t.Fatal("configuration read did not reach the store")
			}
		}
		got := handler.WebhookAllowed(ctx)
		cancel()
		for range cap(store.entered) {
			<-finished
		}
		if got != enabled {
			t.Fatalf("busy refresh changed webhook setting from %t to %t", enabled, got)
		}
	}
}

type uploadBackgroundRunnerStub struct{}

func (uploadBackgroundRunnerStub) Go(_ string, task func()) bool {
	if task != nil {
		go task()
	}
	return task != nil
}

func (uploadAuthorizerStub) Enabled() bool {
	return true
}

func (uploadAuthorizerStub) AuthorizedClientIDs(context.Context, string, *string) ([]string, error) {
	return []string{"client-id"}, nil
}

func TestNewUploadDataHandlerUsesExplicitDependencies(t *testing.T) {
	t.Parallel()

	authorizer := uploadAuthorizerStub{}
	backgroundTasks := uploadBackgroundRunnerStub{}
	dependencies := testUploadDependencies()
	dependencies.OAuth2WebhookAuthorizer = authorizer
	dependencies.BackgroundTasks = backgroundTasks
	handler := newUploadDataHandler(
		&harukiAPIHelper.HarukiToolboxRouterHelpers{},
		dependencies,
	)

	if handler.OAuth2WebhookAuthorizer != authorizer {
		t.Fatalf("OAuth2WebhookAuthorizer = %#v, want explicit dependency %#v", handler.OAuth2WebhookAuthorizer, authorizer)
	}
	if handler.BackgroundTasks != backgroundTasks {
		t.Fatalf("BackgroundTasks = %#v, want explicit dependency %#v", handler.BackgroundTasks, backgroundTasks)
	}
	if handler.HttpClient != dependencies.HTTPClient {
		t.Fatalf("HttpClient = %#v, want explicit dependency %#v", handler.HttpClient, dependencies.HTTPClient)
	}
	if handler.Logger != dependencies.DataHandlerLogger {
		t.Fatalf("Logger = %#v, want explicit dependency %#v", handler.Logger, dependencies.DataHandlerLogger)
	}
	if handler.BirthdaySubscription != dependencies.BirthdaySubscription {
		t.Fatalf("BirthdaySubscription = %#v, want explicit dependency %#v", handler.BirthdaySubscription, dependencies.BirthdaySubscription)
	}
	if handler.SuiteRestoreService != dependencies.SuiteRestoreService {
		t.Fatalf("SuiteRestoreService = %#v, want explicit dependency %#v", handler.SuiteRestoreService, dependencies.SuiteRestoreService)
	}
	if handler.DataSync != dependencies.DataSync {
		t.Fatal("data sync configuration was not injected")
	}
	if !reflect.DeepEqual(handler.ServerCryptor, dependencies.ServerCryptor) {
		t.Fatalf("ServerCryptor = %#v, want explicit dependency %#v", handler.ServerCryptor, dependencies.ServerCryptor)
	}
}

func testUploadDependencies() Dependencies {
	return Dependencies{
		DataSync:          harukiDataHandler.NewDataSyncConfig(harukiConfig.ThirdPartyDataProviderConfig{EndpointSakura: "https://sync.example.test", SecretSakura: "dummy"}),
		HTTPClient:        harukiHttp.NewClient("", 15*time.Second),
		DataHandlerLogger: harukiLogger.NewLogger("UploadTest", "DEBUG", io.Discard),
		BirthdaySubscription: harukiDataHandler.NewBirthdaySubscriptionConfig(harukiDataHandler.BirthdaySubscriptionConfigOptions{
			HMESInternalBaseURL: "https://hmes.example.test",
			HMESInternalToken:   "token",
			UserAgent:           "upload-test",
			RequestTimeout:      3 * time.Second,
		}),
		SuiteRestoreService: harukiDataHandler.NewSuiteRestoreService(harukiDataHandler.SuiteRestoreServiceOptions{}),
		ServerCryptor:       harukiSekai.NewServerCryptor(harukiSekai.ServerCryptorConfig{Regions: map[string]utils.CryptoMaterial{"jp": {Key: testUploadAESKeyHex, IV: testUploadAESIVHex}, "tw": {Key: testUploadAESKeyHex, IV: testUploadAESIVHex}, "kr": {Key: testUploadAESKeyHex, IV: testUploadAESIVHex}, "cn": {Key: testUploadAESKeyHex, IV: testUploadAESIVHex}, "en": {Key: testUploadAESKeyHex, IV: testUploadAESIVHex}}}),
	}
}

const (
	testUploadAESKeyHex = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	testUploadAESIVHex  = "0102030405060708090a0b0c0d0e0f10"
)

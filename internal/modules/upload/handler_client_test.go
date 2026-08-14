package upload

import (
	"context"
	"io"
	"testing"
	"time"

	harukiAPIHelper "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api"
	harukiDataHandler "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/handler"
	harukiHttp "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/http"
	harukiLogger "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/logger"
	harukiSekai "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/sekai"
)

type uploadAuthorizerStub struct{}

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
	if handler.ServerCryptor != dependencies.ServerCryptor {
		t.Fatalf("ServerCryptor = %#v, want explicit dependency %#v", handler.ServerCryptor, dependencies.ServerCryptor)
	}
}

func testUploadDependencies() Dependencies {
	return Dependencies{
		HTTPClient:        harukiHttp.NewClient("", 15*time.Second),
		DataHandlerLogger: harukiLogger.NewLogger("UploadTest", "DEBUG", io.Discard),
		BirthdaySubscription: harukiDataHandler.NewBirthdaySubscriptionConfig(harukiDataHandler.BirthdaySubscriptionConfigOptions{
			HMESInternalBaseURL: "https://hmes.example.test",
			HMESInternalToken:   "token",
			UserAgent:           "upload-test",
			RequestTimeout:      3 * time.Second,
		}),
		SuiteRestoreService: harukiDataHandler.NewSuiteRestoreService(harukiDataHandler.SuiteRestoreServiceOptions{}),
		ServerCryptor: harukiSekai.NewServerCryptor(harukiSekai.ServerCryptorConfig{
			ENServerAESKey:    testUploadAESKeyHex,
			ENServerAESIV:     testUploadAESIVHex,
			OtherServerAESKey: testUploadAESKeyHex,
			OtherServerAESIV:  testUploadAESIVHex,
		}),
	}
}

const (
	testUploadAESKeyHex = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	testUploadAESIVHex  = "0102030405060708090a0b0c0d0e0f10"
)

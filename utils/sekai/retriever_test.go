package sekai

import (
	"context"
	"errors"
	harukiConfig "github.com/Team-Haruki/Haruki-Toolbox-Backend/config"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNewSekaiDataRetriever_InvalidServer(t *testing.T) {
	t.Parallel()

	r := NewSekaiDataRetriever(
		harukiUtils.SupportedInheritUploadServer("kr"),
		harukiUtils.InheritInformation{},
		harukiUtils.UploadDataTypeSuite,
	)
	if !r.isErrorExist {
		t.Fatalf("retriever should be in error state for unsupported server")
	}
	if r.ErrorMessage == "" {
		t.Fatalf("retriever error message should not be empty")
	}
}

func TestRetrieverHelpers_Basic(t *testing.T) {
	t.Parallel()

	if got := upperServerName(JP); got != "JP" {
		t.Fatalf("upperServerName = %q, want JP", got)
	}
	if got := userIDString(123); got != "123" {
		t.Fatalf("userIDString = %q, want 123", got)
	}
	if got := suiteBasePath(1); got != "/suite/user/1" {
		t.Fatalf("suiteBasePath = %q", got)
	}
	if got := suiteInitialPath(JP, 1); got != "/suite/user/1?isLogin=true" {
		t.Fatalf("JP suiteInitialPath = %q", got)
	}
	if got := suiteInitialPath(EN, 1); got != "/suite/user/1" {
		t.Fatalf("EN suiteInitialPath = %q", got)
	}
	if got := suiteFollowupPath(1); got != "/suite/user/1"+retrieverSuiteFollowupQuery {
		t.Fatalf("suiteFollowupPath = %q", got)
	}
	if got := invitationPath(2); got != "/user/2/invitation" {
		t.Fatalf("invitationPath = %q", got)
	}
	if got := homeRefreshPath(3); got != "/user/3/home/refresh" {
		t.Fatalf("homeRefreshPath = %q", got)
	}
	if got := mysekaiPath(4); got != "/user/4/mysekai?isForceAllReloadOnlyMySekai=True" {
		t.Fatalf("mysekaiPath = %q", got)
	}
	if got := mysekaiRoomPath(5); got != "/user/5/mysekai/5/room" {
		t.Fatalf("mysekaiRoomPath = %q", got)
	}
	if got := mysekaiDiarkisPath(6); got != "/user/6/diarkis-auth?diarkisServerType=mysekai" {
		t.Fatalf("mysekaiDiarkisPath = %q", got)
	}
	if got := moduleMaintenancePath(moduleMySekai); got != "/module-maintenance/MYSEKAI" {
		t.Fatalf("moduleMaintenancePath = %q", got)
	}
}

func TestRetrieverHelpers_FlagsAndPayload(t *testing.T) {
	t.Parallel()

	if !hasUserFriends(map[string]any{"userFriends": []any{1}}) {
		t.Fatalf("hasUserFriends should be true")
	}
	if hasUserFriends(map[string]any{"userFriends": nil}) {
		t.Fatalf("hasUserFriends should be false for nil")
	}
	if hasUserFriends(nil) {
		t.Fatalf("hasUserFriends should be false for nil map")
	}

	if !reflect.DeepEqual(selectRefreshPayload(true), RequestDataRefreshLogin) {
		t.Fatalf("selectRefreshPayload(true) should return login payload")
	}
	if !reflect.DeepEqual(selectRefreshPayload(false), RequestDataRefresh) {
		t.Fatalf("selectRefreshPayload(false) should return default payload")
	}

	if !shouldRetrieveMysekai(harukiUtils.UploadDataTypeMysekai) {
		t.Fatalf("shouldRetrieveMysekai(mysekai) should be true")
	}
	if shouldRetrieveMysekai(harukiUtils.UploadDataTypeSuite) {
		t.Fatalf("shouldRetrieveMysekai(suite) should be false")
	}
}

func TestRetrieverHelpers_UnpackAndMaintenance(t *testing.T) {
	originalCfg := harukiConfig.Cfg
	t.Cleanup(func() {
		harukiConfig.Cfg = originalCfg
	})
	harukiConfig.Cfg.SekaiClient.OtherServerAESKey = testAESKeyHex
	harukiConfig.Cfg.SekaiClient.OtherServerAESIV = testAESIVHex
	harukiConfig.Cfg.SekaiClient.ENServerAESKey = testAESKeyHex
	harukiConfig.Cfg.SekaiClient.ENServerAESIV = testAESIVHex

	encrypted, err := Pack(map[string]any{"isOngoing": true}, harukiUtils.SupportedDataUploadServerJP)
	if err != nil {
		t.Fatalf("Pack failed: %v", err)
	}

	m, err := unpackResponseToMap(encrypted, JP)
	if err != nil {
		t.Fatalf("unpackResponseToMap failed: %v", err)
	}
	if m["isOngoing"] != true {
		t.Fatalf("unexpected map value: %v", m["isOngoing"])
	}

	ongoing, err := checkMaintenanceFromBody(encrypted, JP)
	if err != nil {
		t.Fatalf("checkMaintenanceFromBody failed: %v", err)
	}
	if !ongoing {
		t.Fatalf("checkMaintenanceFromBody should return true")
	}
}

func TestRetrieverHelpers_UnpackTypeError(t *testing.T) {
	originalCfg := harukiConfig.Cfg
	t.Cleanup(func() {
		harukiConfig.Cfg = originalCfg
	})
	harukiConfig.Cfg.SekaiClient.OtherServerAESKey = testAESKeyHex
	harukiConfig.Cfg.SekaiClient.OtherServerAESIV = testAESIVHex

	encrypted, err := Pack([]any{1, 2, 3}, harukiUtils.SupportedDataUploadServerJP)
	if err != nil {
		t.Fatalf("Pack failed: %v", err)
	}

	if _, err := unpackResponseToMap(encrypted, JP); err == nil {
		t.Fatalf("unpackResponseToMap should fail for non-map payload")
	}
}

func TestRetrieverRunReturnsErrorForRequiredSuiteFailure(t *testing.T) {
	withTestSekaiCrypto(t)
	withFastRetrieverSleeps(t)

	userID := int64(164337024457871363)
	server := newRetrieverTestServer(t, userID, http.StatusUpgradeRequired, statusCodeOK)
	defer server.Close()

	retriever := newTestRetriever(server.URL, userID)
	result, err := retriever.Run(context.Background())
	if err == nil {
		t.Fatalf("Run should fail when required suite retrieval fails")
	}
	if result == nil || result.UserID != userID {
		t.Fatalf("Run partial result userID = %#v, want %d", result, userID)
	}
	if result.Suite != nil {
		t.Fatalf("Run partial result suite should be nil on suite retrieval failure")
	}
	var retrievalErr *DataRetrievalError
	if !errors.As(err, &retrievalErr) || retrievalErr.DataType != string(harukiUtils.UploadDataTypeSuite) {
		t.Fatalf("Run error = %v, want suite DataRetrievalError", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("Run error = %v, want APIError status %d", err, http.StatusUpgradeRequired)
	}
}

func TestRetrieverRunIgnoresFinalHomeRefreshFailure(t *testing.T) {
	withTestSekaiCrypto(t)
	withFastRetrieverSleeps(t)

	userID := int64(164337024457871363)
	server := newRetrieverTestServer(t, userID, statusCodeOK, statusCodeForbidden)
	defer server.Close()

	retriever := newTestRetriever(server.URL, userID)
	result, err := retriever.Run(context.Background())
	if err != nil {
		t.Fatalf("Run should ignore final home refresh failure, got %v", err)
	}
	if result == nil || result.UserID != userID || len(result.Suite) == 0 {
		t.Fatalf("Run result = %#v, want userID and suite payload", result)
	}
}

func withTestSekaiCrypto(t *testing.T) {
	t.Helper()
	originalCfg := harukiConfig.Cfg
	t.Cleanup(func() {
		harukiConfig.Cfg = originalCfg
	})
	harukiConfig.Cfg.SekaiClient.OtherServerAESKey = testAESKeyHex
	harukiConfig.Cfg.SekaiClient.OtherServerAESIV = testAESIVHex
	harukiConfig.Cfg.SekaiClient.ENServerAESKey = testAESKeyHex
	harukiConfig.Cfg.SekaiClient.ENServerAESIV = testAESIVHex
}

func withFastRetrieverSleeps(t *testing.T) {
	t.Helper()
	originalClientSleep := clientSleep
	originalRetrieverSleep := retrieverSleep
	clientSleep = func(time.Duration) {}
	retrieverSleep = func(time.Duration) {}
	t.Cleanup(func() {
		clientSleep = originalClientSleep
		retrieverSleep = originalRetrieverSleep
	})
}

func newTestRetriever(baseURL string, userID int64) *HarukiSekaiDataRetriever {
	client := NewSekaiClientWithConfig(ClientConfig{
		Server:          EN,
		API:             baseURL + "/api",
		VersionURL:      baseURL + "/version",
		Inherit:         harukiUtils.InheritInformation{InheritID: "inherit-id", InheritPassword: "password"},
		Headers:         map[string]string{},
		InheritJWTToken: "secret",
	})
	return &HarukiSekaiDataRetriever{
		client:     client,
		uploadType: harukiUtils.UploadDataTypeSuite,
		logger:     client.logger,
	}
}

func newRetrieverTestServer(t *testing.T, userID int64, suiteStatus int, homeRefreshStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/version":
			_, _ = w.Write([]byte(`{"appVersion":"1","appHash":"2","dataVersion":"3","assetVersion":"4"}`))
		case strings.HasPrefix(r.URL.Path, "/api/inherit/user/") && r.URL.Query().Get("isExecuteInherit") == "False":
			writePackedTestResponse(t, w, map[string]any{
				"afterUserGamedata": map[string]any{"userId": userID},
			})
		case strings.HasPrefix(r.URL.Path, "/api/inherit/user/") && r.URL.Query().Get("isExecuteInherit") == "True":
			writePackedTestResponse(t, w, map[string]any{"credential": "credential"})
		case r.URL.Path == "/api/user/"+userIDString(userID)+"/auth":
			writePackedTestResponse(t, w, map[string]any{"sessionToken": "session-token"})
		case r.URL.Path == "/api/suite/user/"+userIDString(userID):
			w.WriteHeader(suiteStatus)
			if suiteStatus == statusCodeOK {
				writePackedTestResponse(t, w, map[string]any{"userId": userID})
			}
		case r.URL.Path == "/api/user/"+userIDString(userID)+"/home/refresh":
			w.WriteHeader(homeRefreshStatus)
			if homeRefreshStatus == statusCodeOK {
				writePackedTestResponse(t, w, map[string]any{"ok": true})
			}
		default:
			writePackedTestResponse(t, w, map[string]any{"ok": true})
		}
	}))
}

func writePackedTestResponse(t *testing.T, w http.ResponseWriter, payload map[string]any) {
	t.Helper()
	packed, err := Pack(payload, harukiUtils.SupportedDataUploadServerEN)
	if err != nil {
		t.Fatalf("Pack test response failed: %v", err)
	}
	_, _ = w.Write(packed)
}

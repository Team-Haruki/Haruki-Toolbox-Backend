package sekai

import (
	harukiConfig "haruki-suite/config"
	harukiUtils "haruki-suite/utils"
	"reflect"
	"testing"
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

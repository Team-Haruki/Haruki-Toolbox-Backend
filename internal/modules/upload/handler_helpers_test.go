package upload

import (
	harukiConfig "haruki-suite/config"
	harukiSchema "haruki-suite/ent/schema"
	harukiUtils "haruki-suite/utils"
	harukiAPIHelper "haruki-suite/utils/api"
	"testing"
)

func TestExtractUploadTypeAndUserID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		url     string
		wantTyp harukiUtils.UploadDataType
		wantID  int64
	}{
		{
			name:    "suite upload",
			url:     "/api/upload/manual/jp/suite/user/123456",
			wantTyp: harukiUtils.UploadDataTypeSuite,
			wantID:  123456,
		},
		{
			name:    "mysekai birthday party upload",
			url:     "/api/upload/manual/jp/mysekai/birthday-party/user/88",
			wantTyp: harukiUtils.UploadDataTypeMysekaiBirthdayParty,
			wantID:  88,
		},
		{
			name:    "mysekai upload",
			url:     "/api/upload/manual/jp/mysekai/user/999",
			wantTyp: harukiUtils.UploadDataTypeMysekai,
			wantID:  999,
		},
		{
			name:    "invalid path",
			url:     "/api/upload/manual/jp/suite/without-user-id",
			wantTyp: "",
			wantID:  0,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotType, gotID := ExtractUploadTypeAndUserID(tc.url)
			if gotType != tc.wantTyp || gotID != tc.wantID {
				t.Fatalf("ExtractUploadTypeAndUserID(%q) = (%q, %d), want (%q, %d)", tc.url, gotType, gotID, tc.wantTyp, tc.wantID)
			}
		})
	}
}

func TestDeterminePublicAPIPermission(t *testing.T) {
	t.Parallel()

	settings := harukiAPIHelper.HarukiToolboxGameAccountPrivacySettings{
		Suite:   &harukiSchema.SuiteDataPrivacySettings{AllowPublicApi: true},
		Mysekai: &harukiSchema.MysekaiDataPrivacySettings{AllowPublicApi: false},
	}

	if !determinePublicAPIPermission(true, harukiUtils.UploadDataTypeSuite, settings) {
		t.Fatalf("suite permission should be enabled")
	}
	if determinePublicAPIPermission(true, harukiUtils.UploadDataTypeMysekai, settings) {
		t.Fatalf("mysekai permission should be disabled")
	}
	if determinePublicAPIPermission(false, harukiUtils.UploadDataTypeSuite, settings) {
		t.Fatalf("non-existing binding should not allow public api")
	}
}

func TestValidateCNMysekaiAccess(t *testing.T) {
	t.Parallel()

	userID := "1241241241"
	deny := false
	allow := true

	if err := validateCNMysekaiAccess(harukiUtils.UploadDataTypeMysekai, harukiUtils.SupportedDataUploadServerCN, &userID, &deny); err == nil {
		t.Fatalf("expected illegal request when cn mysekai is disabled")
	}
	if err := validateCNMysekaiAccess(harukiUtils.UploadDataTypeMysekai, harukiUtils.SupportedDataUploadServerCN, &userID, &allow); err != nil {
		t.Fatalf("unexpected error when cn mysekai is enabled: %v", err)
	}
	if err := validateCNMysekaiAccess(harukiUtils.UploadDataTypeSuite, harukiUtils.SupportedDataUploadServerCN, &userID, &deny); err != nil {
		t.Fatalf("suite uploads should not be blocked by cn mysekai flag: %v", err)
	}
}

func TestValidateUploadResult(t *testing.T) {
	t.Parallel()

	statusOK := 200
	statusFail := 500

	if err := validateUploadResult(&harukiUtils.HandleDataResult{Status: &statusOK}); err != nil {
		t.Fatalf("unexpected error for status 200: %v", err)
	}
	if err := validateUploadResult(&harukiUtils.HandleDataResult{Status: &statusFail}); err == nil {
		t.Fatalf("expected error for non-200 status")
	}
}

func TestValidateGameAccountBelonging(t *testing.T) {
	t.Parallel()

	belongs := true
	if err := validateGameAccountBelonging(&belongs); err != nil {
		t.Fatalf("unexpected error when account belongs to user: %v", err)
	}

	notBelong := false
	if err := validateGameAccountBelonging(&notBelong); err == nil {
		t.Fatalf("expected error when account belongs to another user")
	}
}

func TestDeriveUploadOwnership(t *testing.T) {
	t.Parallel()

	owner := "u1"
	if got := deriveUploadOwnership("", nil, harukiUtils.UploadMethodManual); got != nil {
		t.Fatalf("expected nil ownership for unbound account")
	}
	if got := deriveUploadOwnership(owner, nil, harukiUtils.UploadMethodManual); got == nil || *got {
		t.Fatalf("expected anonymous upload to owned account to be rejected")
	}
	if got := deriveUploadOwnership(owner, &owner, harukiUtils.UploadMethodManual); got == nil || !*got {
		t.Fatalf("expected owner upload to be accepted")
	}
	other := "u2"
	if got := deriveUploadOwnership(owner, &other, harukiUtils.UploadMethodManual); got == nil || *got {
		t.Fatalf("expected different user upload to be rejected")
	}
	if got := deriveUploadOwnership(owner, nil, harukiUtils.UploadMethodIOSProxy); got != nil {
		t.Fatalf("expected trusted anonymous upload to skip ownership enforcement")
	}
	if got := deriveUploadOwnership(owner, nil, harukiUtils.UploadMethodInherit); got != nil {
		t.Fatalf("expected inherit upload to skip ownership enforcement")
	}
}

func TestMapUploadProcessingError(t *testing.T) {
	t.Parallel()

	if got := mapUploadProcessingError(errUploadOwnershipMismatch); got == nil || got.Code != 403 {
		t.Fatalf("expected ownership mismatch to map to 403, got %#v", got)
	}
	if got := mapUploadProcessingError(errUploadOwnerBanned); got == nil || got.Code != 403 {
		t.Fatalf("expected banned owner to map to 403, got %#v", got)
	}
	if got := mapUploadProcessingError(errUploadCNMysekaiDenied); got == nil || got.Code != 403 {
		t.Fatalf("expected cn mysekai deny to map to 403, got %#v", got)
	}
	if got := mapUploadProcessingError(nil); got != nil {
		t.Fatalf("expected nil error to stay nil, got %#v", got)
	}
}

func TestGetSharedHTTPClientReloadsOnProxyChange(t *testing.T) {
	originalCfg := harukiConfig.Cfg
	sharedHttpClientMu.Lock()
	originalClient := sharedHttpClient
	originalProxy := sharedHttpClientProxy
	sharedHttpClient = nil
	sharedHttpClientProxy = ""
	sharedHttpClientMu.Unlock()

	t.Cleanup(func() {
		harukiConfig.Cfg = originalCfg
		sharedHttpClientMu.Lock()
		sharedHttpClient = originalClient
		sharedHttpClientProxy = originalProxy
		sharedHttpClientMu.Unlock()
	})

	harukiConfig.Cfg.Proxy = "http://127.0.0.1:8080"
	first := getSharedHTTPClient()
	if first == nil {
		t.Fatalf("expected non-nil http client")
	}
	second := getSharedHTTPClient()
	if first != second {
		t.Fatalf("expected shared client to be reused when proxy does not change")
	}

	harukiConfig.Cfg.Proxy = "http://127.0.0.1:18080"
	third := getSharedHTTPClient()
	if third == nil {
		t.Fatalf("expected non-nil reloaded http client")
	}
	if third == first {
		t.Fatalf("expected shared client to reload after proxy change")
	}
}

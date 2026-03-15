package handler

import (
	"context"
	"encoding/json"
	harukiConfig "haruki-suite/config"
	harukiUtils "haruki-suite/utils"
	harukiLogger "haruki-suite/utils/logger"
	"io"
	"net"
	"testing"
)

func testLogger() *harukiLogger.Logger {
	return harukiLogger.NewLogger("handler-test", "DEBUG", io.Discard)
}

func TestConvertToStatusCode(t *testing.T) {
	t.Parallel()

	logger := testLogger()
	tests := []struct {
		name string
		in   any
		want int
	}{
		{name: "float64", in: float64(404), want: 404},
		{name: "int64", in: int64(500), want: 500},
		{name: "uint32", in: uint32(201), want: 201},
		{name: "json number", in: json.Number("503"), want: 503},
		{name: "unknown type", in: "not-status", want: 0},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := convertToStatusCode(tc.in, logger); got != tc.want {
				t.Fatalf("convertToStatusCode(%v) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestConvertToInt64Pointer(t *testing.T) {
	t.Parallel()

	logger := testLogger()
	tests := []struct {
		name    string
		in      any
		wantNil bool
		want    int64
	}{
		{name: "json number", in: json.Number("123"), want: 123},
		{name: "string", in: "456", want: 456},
		{name: "float64", in: float64(789), want: 789},
		{name: "int64", in: int64(321), want: 321},
		{name: "uint64", in: uint64(654), want: 654},
		{name: "negative string", in: "-1", wantNil: true},
		{name: "unsupported", in: true, wantNil: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := convertToInt64Pointer(tc.in, logger)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("convertToInt64Pointer(%v) should be nil, got %d", tc.in, *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("convertToInt64Pointer(%v) = nil, want %d", tc.in, tc.want)
			}
			if *got != tc.want {
				t.Fatalf("convertToInt64Pointer(%v) = %d, want %d", tc.in, *got, tc.want)
			}
		})
	}
}

func TestParseWebhookCallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		in         map[string]any
		wantURL    string
		wantBearer string
		wantOK     bool
	}{
		{
			name:   "missing callback_url",
			in:     map[string]any{"bearer": "token"},
			wantOK: false,
		},
		{
			name:   "callback_url invalid type",
			in:     map[string]any{"callback_url": 123},
			wantOK: false,
		},
		{
			name:   "callback_url empty",
			in:     map[string]any{"callback_url": " "},
			wantOK: false,
		},
		{
			name:       "lowercase bearer",
			in:         map[string]any{"callback_url": "https://93.184.216.34", "bearer": "abc"},
			wantURL:    "https://93.184.216.34",
			wantBearer: "abc",
			wantOK:     true,
		},
		{
			name:       "legacy Bearer field fallback",
			in:         map[string]any{"callback_url": "https://93.184.216.34", "Bearer": "legacy"},
			wantURL:    "https://93.184.216.34",
			wantBearer: "legacy",
			wantOK:     true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotURL, gotBearer, gotOK := parseWebhookCallback(tc.in)
			if gotOK != tc.wantOK {
				t.Fatalf("parseWebhookCallback ok = %v, want %v", gotOK, tc.wantOK)
			}
			if gotURL != tc.wantURL {
				t.Fatalf("parseWebhookCallback url = %q, want %q", gotURL, tc.wantURL)
			}
			if gotBearer != tc.wantBearer {
				t.Fatalf("parseWebhookCallback bearer = %q, want %q", gotBearer, tc.wantBearer)
			}
		})
	}
}

func TestValidateWebhookCallbackURLRejectsResolvedPrivateIP(t *testing.T) {
	originalLookup := webhookIPAddrLookup
	webhookIPAddrLookup = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	defer func() {
		webhookIPAddrLookup = originalLookup
	}()

	if _, ok := validateWebhookCallbackURL("https://example.com/callback"); ok {
		t.Fatalf("expected callback URL resolving to loopback IP to be rejected")
	}
}

func TestValidateWebhookCallbackURLAcceptsResolvedPublicIP(t *testing.T) {
	originalLookup := webhookIPAddrLookup
	webhookIPAddrLookup = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	defer func() {
		webhookIPAddrLookup = originalLookup
	}()

	if got, ok := validateWebhookCallbackURL("https://example.com/callback"); !ok || got != "https://example.com/callback" {
		t.Fatalf("validateWebhookCallbackURL returned (%q, %v), want (%q, true)", got, ok, "https://example.com/callback")
	}
}

func TestValidateJPENImagePath(t *testing.T) {
	t.Parallel()

	valid := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	if err := validateJPENImagePath(valid, harukiUtils.SupportedDataUploadServerJP); err != nil {
		t.Fatalf("validateJPENImagePath(valid) returned error: %v", err)
	}
	if err := validateJPENImagePath("not-valid", harukiUtils.SupportedDataUploadServerEN); err == nil {
		t.Fatalf("validateJPENImagePath should fail on invalid pattern")
	}
}

func TestValidateUserIDMatch(t *testing.T) {
	t.Parallel()

	expected := int64(100)
	same := int64(100)
	different := int64(101)

	if err := validateUserIDMatch(&expected, &same, harukiUtils.UploadDataTypeSuite); err != nil {
		t.Fatalf("validateUserIDMatch should pass when IDs match: %v", err)
	}
	if err := validateUserIDMatch(&expected, &different, harukiUtils.UploadDataTypeSuite); err == nil {
		t.Fatalf("validateUserIDMatch should fail on suite user mismatch")
	}
	if err := validateUserIDMatch(&expected, &different, harukiUtils.UploadDataTypeMysekai); err != nil {
		t.Fatalf("validateUserIDMatch should ignore mismatch for mysekai: %v", err)
	}
}

func TestValidateSuiteData(t *testing.T) {
	t.Parallel()

	if err := validateSuiteData(map[string]any{"userGamedata": map[string]any{}}); err != nil {
		t.Fatalf("validateSuiteData should accept userGamedata: %v", err)
	}
	if err := validateSuiteData(map[string]any{"userProfile": map[string]any{}}); err != nil {
		t.Fatalf("validateSuiteData should accept userProfile: %v", err)
	}
	if err := validateSuiteData(map[string]any{"other": true}); err == nil {
		t.Fatalf("validateSuiteData should fail when both userGamedata and userProfile are missing")
	}
}

func TestExtractBirthdayPartyData(t *testing.T) {
	t.Parallel()

	harvestMaps := []any{map[string]any{"partyId": 1}}
	in := map[string]any{
		"updatedResources": map[string]any{
			"userMysekaiHarvestMaps": harvestMaps,
			"otherField":             "ignored",
		},
	}
	out := extractBirthdayPartyData(in)

	updated, ok := out["updatedResources"].(map[string]any)
	if !ok {
		t.Fatalf("updatedResources should be a map, got %T", out["updatedResources"])
	}
	if got := updated["userMysekaiHarvestMaps"]; got == nil {
		t.Fatalf("userMysekaiHarvestMaps should be present in extracted payload")
	}
	if _, ok := updated["otherField"]; ok {
		t.Fatalf("other fields should be stripped from extracted payload")
	}
}

func TestShouldRestoreSuiteForDB(t *testing.T) {
	original := harukiConfig.Cfg.RestoreSuite.EnableRegions
	defer func() { harukiConfig.Cfg.RestoreSuite.EnableRegions = original }()

	harukiConfig.Cfg.RestoreSuite.EnableRegions = []string{"jp", "en"}

	if !shouldRestoreSuiteForDB(harukiUtils.SupportedDataUploadServerJP) {
		t.Fatalf("shouldRestoreSuiteForDB should return true for configured region jp")
	}
	if shouldRestoreSuiteForDB(harukiUtils.SupportedDataUploadServerKR) {
		t.Fatalf("shouldRestoreSuiteForDB should return false for non-configured region kr")
	}
}

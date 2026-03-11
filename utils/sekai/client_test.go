package sekai

import (
	"errors"
	harukiUtils "haruki-suite/utils"
	"testing"
)

func TestCloneHeaders(t *testing.T) {
	t.Parallel()

	src := map[string]string{"A": "1"}
	dst := cloneHeaders(src)
	dst["A"] = "2"
	if src["A"] != "1" {
		t.Fatalf("cloneHeaders should not mutate source map")
	}

	empty := cloneHeaders(nil)
	if empty == nil {
		t.Fatalf("cloneHeaders(nil) should return non-nil map")
	}
}

func TestBuildInheritPath(t *testing.T) {
	t.Parallel()

	gotJP := buildInheritPath(JP, "abc", true)
	if gotJP != "/inherit/user/abc?isExecuteInherit=False" {
		t.Fatalf("JP inherit path = %q", gotJP)
	}

	gotEN := buildInheritPath(EN, "abc", false)
	if gotEN != "/inherit/user/abc?isExecuteInherit=True&isAdult=True&tAge=16" {
		t.Fatalf("EN inherit path = %q", gotEN)
	}
}

func TestExtractInheritUserID(t *testing.T) {
	t.Parallel()

	id, err := extractInheritUserID(map[string]any{
		"afterUserGamedata": map[string]any{"userId": int64(123)},
	})
	if err != nil || id != 123 {
		t.Fatalf("extractInheritUserID int64 got=%d err=%v", id, err)
	}

	id, err = extractInheritUserID(map[string]any{
		"afterUserGamedata": map[string]any{"userId": uint64(456)},
	})
	if err != nil || id != 456 {
		t.Fatalf("extractInheritUserID uint64 got=%d err=%v", id, err)
	}

	_, err = extractInheritUserID(map[string]any{"afterUserGamedata": map[string]any{"userId": "bad"}})
	if err == nil {
		t.Fatalf("extractInheritUserID should fail for unsupported type")
	}
}

func TestExtractInheritCredentialAndSessionToken(t *testing.T) {
	t.Parallel()

	cred, err := extractInheritCredential(map[string]any{"credential": "token"})
	if err != nil || cred != "token" {
		t.Fatalf("extractInheritCredential got=%q err=%v", cred, err)
	}
	if _, err := extractInheritCredential(map[string]any{}); err == nil {
		t.Fatalf("extractInheritCredential should fail when missing")
	}

	session, err := extractSessionToken(map[string]any{"sessionToken": "s"})
	if err != nil || session != "s" {
		t.Fatalf("extractSessionToken got=%q err=%v", session, err)
	}
	if _, err := extractSessionToken(map[string]any{"sessionToken": ""}); err == nil {
		t.Fatalf("extractSessionToken should fail for empty token")
	}
}

func TestMergedHeadersAndApplySessionHeaders(t *testing.T) {
	t.Parallel()

	base := map[string]string{"A": "1"}
	custom := map[string]string{"B": "2", "A": "3"}

	merged := mergedHeaders(base, custom)
	if merged["A"] != "3" || merged["B"] != "2" {
		t.Fatalf("mergedHeaders result = %#v", merged)
	}
	if base["A"] != "1" {
		t.Fatalf("mergedHeaders should not mutate base map")
	}

	client := &HarukiSekaiClient{}
	dst := map[string]string{}
	applySessionHeaders(dst, map[string]string{
		headerSessionToken: "st",
		headerLoginBonus:   "true",
	}, client)
	if dst[headerSessionToken] != "st" {
		t.Fatalf("session header not applied")
	}
	if !client.loginBonus {
		t.Fatalf("loginBonus should be true")
	}
}

func TestParseVersionPayloadAndHeaders(t *testing.T) {
	t.Parallel()

	payload, err := parseVersionPayload([]byte(`{"appVersion":"1","appHash":"2","dataVersion":"3","assetVersion":"4"}`))
	if err != nil {
		t.Fatalf("parseVersionPayload err=%v", err)
	}
	headers := map[string]string{}
	applyVersionHeaders(headers, payload)
	if headers["X-App-Version"] != "1" || headers["X-App-Hash"] != "2" || headers["X-Data-Version"] != "3" || headers["X-Asset-Version"] != "4" {
		t.Fatalf("applyVersionHeaders result = %#v", headers)
	}
}

func TestGetServerConfig_InvalidServer(t *testing.T) {
	t.Parallel()

	_, err := GetServerConfig(harukiUtils.SupportedInheritUploadServer("kr"))
	if err == nil {
		t.Fatalf("GetServerConfig should fail for unsupported server")
	}
	if !errors.Is(err, ErrInvalidServer) {
		t.Fatalf("GetServerConfig err=%v, want ErrInvalidServer", err)
	}
}

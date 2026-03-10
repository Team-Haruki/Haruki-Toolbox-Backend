package usersocial

import (
	harukiAPIHelper "haruki-suite/utils/api"
	"testing"
)

func TestIsSupportedSocialPlatform(t *testing.T) {
	t.Parallel()

	if !isSupportedSocialPlatform(harukiAPIHelper.SocialPlatformQQ) {
		t.Fatalf("qq should be supported")
	}
	if !isSupportedSocialPlatform(harukiAPIHelper.SocialPlatformQQBot) {
		t.Fatalf("qq_bot should be supported")
	}
	if !isSupportedSocialPlatform(harukiAPIHelper.SocialPlatformDiscord) {
		t.Fatalf("discord should be supported")
	}
	if !isSupportedSocialPlatform(harukiAPIHelper.SocialPlatformTelegram) {
		t.Fatalf("telegram should be supported")
	}
	if isSupportedSocialPlatform(harukiAPIHelper.SocialPlatform("wechat")) {
		t.Fatalf("wechat should not be supported")
	}
}

func TestExtractBearerToken(t *testing.T) {
	t.Parallel()

	token, ok := extractBearerToken("Bearer abc123")
	if !ok {
		t.Fatalf("expected bearer token to parse")
	}
	if token != "abc123" {
		t.Fatalf("token = %q, want %q", token, "abc123")
	}

	token, ok = extractBearerToken("Bearer   spaced-token   ")
	if !ok {
		t.Fatalf("expected bearer token with spaces to parse")
	}
	if token != "spaced-token" {
		t.Fatalf("token = %q, want %q", token, "spaced-token")
	}

	if _, ok = extractBearerToken(""); ok {
		t.Fatalf("empty header should fail")
	}
	token, ok = extractBearerToken("bearer lowercase")
	if !ok {
		t.Fatalf("lowercase bearer scheme should parse")
	}
	if token != "lowercase" {
		t.Fatalf("token = %q, want %q", token, "lowercase")
	}
	if _, ok = extractBearerToken("Basic abc123"); ok {
		t.Fatalf("non-bearer header should fail")
	}
	if _, ok = extractBearerToken("Bearer    "); ok {
		t.Fatalf("empty bearer token should fail")
	}
}

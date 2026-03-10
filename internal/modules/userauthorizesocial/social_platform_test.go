package userauthorizesocial

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
		t.Fatalf("qqbot should be supported")
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

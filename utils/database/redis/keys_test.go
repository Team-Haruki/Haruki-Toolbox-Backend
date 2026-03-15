package redis

import (
	harukiConfig "haruki-suite/config"
	"testing"
)

func TestBuildKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "email verify",
			got:  BuildEmailVerifyKey(" A@Example.Com "),
			want: "haruki:email:verify:" + hashNormalizedIdentifier(" A@Example.Com "),
		},
		{
			name: "reset password",
			got:  BuildResetPasswordKey(" A@Example.Com "),
			want: "haruki:email:reset-password:" + hashNormalizedIdentifier(" A@Example.Com "),
		},
		{
			name: "game account verify",
			got:  BuildGameAccountVerifyKey("u1", "jp", "123"),
			want: "haruki:game-account:verify:u1:jp:123",
		},
		{
			name: "game account verify attempt",
			got:  BuildGameAccountVerifyAttemptKey("u1", "jp", "123"),
			want: "haruki:game-account:verify:attempt:u1:jp:123",
		},
		{
			name: "social platform verify",
			got:  BuildSocialPlatformVerifyKey("discord", "42"),
			want: "haruki:social:verify:discord:42",
		},
		{
			name: "social platform user id",
			got:  BuildSocialPlatformUserIDKey("discord", "42"),
			want: "haruki:social:verify:discord:42:user-id",
		},
		{
			name: "social platform status token",
			got:  BuildSocialPlatformStatusTokenKey("discord", "42"),
			want: "haruki:social:verify:discord:42:status-token",
		},
		{
			name: "status token",
			got:  BuildStatusTokenKey("token-abc"),
			want: "haruki:social:status-token:token-abc",
		},
		{
			name: "status token owner",
			got:  BuildStatusTokenOwnerKey("token-abc"),
			want: "haruki:social:status-token:token-abc:user-id",
		},
		{
			name: "status token binding",
			got:  BuildStatusTokenBindingKey("token-abc"),
			want: "haruki:social:status-token:token-abc:binding",
		},
		{
			name: "otp attempt",
			got:  BuildOTPAttemptKey(" A@Example.Com "),
			want: "haruki:email:attempt:" + hashNormalizedIdentifier(" A@Example.Com "),
		},
		{
			name: "email verify send rate limit ip",
			got:  BuildEmailVerifySendRateLimitIPKey("127.0.0.1"),
			want: "haruki:email:verify:send:ip:127.0.0.1",
		},
		{
			name: "email verify send rate limit target",
			got:  BuildEmailVerifySendRateLimitTargetKey(" A@Example.Com "),
			want: "haruki:email:verify:send:target:" + hashNormalizedIdentifier(" A@Example.Com "),
		},
		{
			name: "reset password send rate limit ip",
			got:  BuildResetPasswordSendRateLimitIPKey("127.0.0.1"),
			want: "haruki:email:reset-password:send:ip:127.0.0.1",
		},
		{
			name: "reset password send rate limit target",
			got:  BuildResetPasswordSendRateLimitTargetKey(" A@Example.Com "),
			want: "haruki:email:reset-password:send:target:" + hashNormalizedIdentifier(" A@Example.Com "),
		},
		{
			name: "reset password apply rate limit ip",
			got:  BuildResetPasswordApplyRateLimitIPKey("127.0.0.1"),
			want: "haruki:email:reset-password:attempt:ip:127.0.0.1",
		},
		{
			name: "reset password apply rate limit target",
			got:  BuildResetPasswordApplyRateLimitTargetKey(" A@Example.Com "),
			want: "haruki:email:reset-password:attempt:target:" + hashNormalizedIdentifier(" A@Example.Com "),
		},
		{
			name: "login rate limit ip",
			got:  BuildLoginRateLimitIPKey("127.0.0.1"),
			want: "haruki:email:login:attempt:ip:127.0.0.1",
		},
		{
			name: "login rate limit target",
			got:  BuildLoginRateLimitTargetKey(" A@Example.Com "),
			want: "haruki:email:login:attempt:target:" + hashNormalizedIdentifier(" A@Example.Com "),
		},
		{
			name: "upload ingress rate limit",
			got:  BuildUploadIngressRateLimitKey(1700000000, "127.0.0.1|POST|/inherit/:server/:upload_type/submit"),
			want: "haruki:rate-limit:upload-ingress:1700000000:127.0.0.1|POST|/inherit/:server/:upload_type/submit",
		},
		{
			name: "ios upload chunk meta",
			got:  BuildIOSUploadChunkMetaKey("toolbox-user|jp|123456|upload-id"),
			want: "haruki:upload:ios:chunk-meta:toolbox-user|jp|123456|upload-id",
		},
		{
			name: "ios upload chunk data",
			got:  BuildIOSUploadChunkDataKey("toolbox-user|jp|123456|upload-id"),
			want: "haruki:upload:ios:chunk-data:toolbox-user|jp|123456|upload-id",
		},
		{
			name: "ios upload chunk claim",
			got:  BuildIOSUploadChunkClaimKey("toolbox-user|jp|123456|upload-id"),
			want: "haruki:upload:ios:chunk-claim:toolbox-user|jp|123456|upload-id",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got != tc.want {
				t.Fatalf("%s key = %q, want %q", tc.name, tc.got, tc.want)
			}
		})
	}
}

func TestBuildKeyHelper(t *testing.T) {
	t.Parallel()

	if got := buildKey("a", "b", "c"); got != "a:b:c" {
		t.Fatalf("buildKey() = %q, want %q", got, "a:b:c")
	}
	if got := buildKey("single"); got != "single" {
		t.Fatalf("buildKey(single) = %q, want %q", got, "single")
	}
}

func TestHashNormalizedIdentifier(t *testing.T) {
	t.Parallel()

	a := hashNormalizedIdentifier(" A@Example.Com ")
	b := hashNormalizedIdentifier("a@example.com")
	if a == "" {
		t.Fatalf("hashNormalizedIdentifier should not return empty string")
	}
	if a != b {
		t.Fatalf("hashNormalizedIdentifier should be case/space insensitive: %q != %q", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("hashNormalizedIdentifier length = %d, want 64", len(a))
	}
}

func TestHashNormalizedIdentifierUsesSecret(t *testing.T) {
	prevCfg := harukiConfig.Cfg
	t.Cleanup(func() { harukiConfig.Cfg = prevCfg })

	harukiConfig.Cfg.UserSystem.SessionSignToken = "secret-a"
	a := hashNormalizedIdentifier("sensitive@example.com")
	harukiConfig.Cfg.UserSystem.SessionSignToken = "secret-b"
	b := hashNormalizedIdentifier("sensitive@example.com")
	if a == b {
		t.Fatalf("hash should differ when session_sign_token changes")
	}
}

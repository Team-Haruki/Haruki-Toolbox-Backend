package redis

import (
	"strings"
	"testing"
)

func TestBuildKeys(t *testing.T) {
	t.Parallel()
	keys := NewKeyBuilder("")

	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "email verify",
			got:  keys.BuildEmailVerifyKey(" A@Example.Com "),
			want: "haruki:email:verify:" + keys.hashNormalizedIdentifier(" A@Example.Com "),
		},
		{
			name: "reset password",
			got:  keys.BuildResetPasswordKey(" A@Example.Com "),
			want: "haruki:email:reset-password:" + keys.hashNormalizedIdentifier(" A@Example.Com "),
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
			name: "qq mail send rate limit user",
			got:  BuildQQMailSendRateLimitUserKey("u1"),
			want: "haruki:social:qq-mail:send:user:u1",
		},
		{
			name: "qq mail send rate limit target",
			got:  keys.BuildQQMailSendRateLimitTargetKey(" 123456 "),
			want: "haruki:social:qq-mail:send:target:" + keys.hashNormalizedIdentifier(" 123456 "),
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
			got:  keys.BuildOTPAttemptKey(" A@Example.Com "),
			want: "haruki:email:attempt:" + keys.hashNormalizedIdentifier(" A@Example.Com "),
		},
		{
			name: "email verify send rate limit ip",
			got:  BuildEmailVerifySendRateLimitIPKey("127.0.0.1"),
			want: "haruki:email:verify:send:ip:127.0.0.1",
		},
		{
			name: "email verify send rate limit target",
			got:  keys.BuildEmailVerifySendRateLimitTargetKey(" A@Example.Com "),
			want: "haruki:email:verify:send:target:" + keys.hashNormalizedIdentifier(" A@Example.Com "),
		},
		{
			name: "reset password send rate limit ip",
			got:  BuildResetPasswordSendRateLimitIPKey("127.0.0.1"),
			want: "haruki:email:reset-password:send:ip:127.0.0.1",
		},
		{
			name: "reset password send rate limit target",
			got:  keys.BuildResetPasswordSendRateLimitTargetKey(" A@Example.Com "),
			want: "haruki:email:reset-password:send:target:" + keys.hashNormalizedIdentifier(" A@Example.Com "),
		},
		{
			name: "reset password apply rate limit ip",
			got:  BuildResetPasswordApplyRateLimitIPKey("127.0.0.1"),
			want: "haruki:email:reset-password:attempt:ip:127.0.0.1",
		},
		{
			name: "reset password apply rate limit target",
			got:  keys.BuildResetPasswordApplyRateLimitTargetKey(" A@Example.Com "),
			want: "haruki:email:reset-password:attempt:target:" + keys.hashNormalizedIdentifier(" A@Example.Com "),
		},
		{
			name: "login rate limit ip",
			got:  BuildLoginRateLimitIPKey("127.0.0.1"),
			want: "haruki:email:login:attempt:ip:127.0.0.1",
		},
		{
			name: "login rate limit target",
			got:  keys.BuildLoginRateLimitTargetKey(" A@Example.Com "),
			want: "haruki:email:login:attempt:target:" + keys.hashNormalizedIdentifier(" A@Example.Com "),
		},
		{
			name: "upload ingress rate limit",
			got:  BuildUploadIngressRateLimitKey(1700000000, "127.0.0.1|POST|/api/inherit/:server/:upload_type"),
			want: "haruki:rate-limit:upload-ingress:1700000000:127.0.0.1|POST|/api/inherit/:server/:upload_type",
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

	keys := NewKeyBuilder("")
	a := keys.hashNormalizedIdentifier(" A@Example.Com ")
	b := keys.hashNormalizedIdentifier("a@example.com")
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
	a := NewKeyBuilder("secret-a").hashNormalizedIdentifier("sensitive@example.com")
	b := NewKeyBuilder("secret-b").hashNormalizedIdentifier("sensitive@example.com")
	if want := "804980c27f960e34d6bc09706c2e176c1f0d00824ed82bad3247506c82aa58f4"; a != want {
		t.Fatalf("secret-a hash = %q, want historical HMAC-SHA256 %q", a, want)
	}
	if a == b {
		t.Fatalf("hash should differ when session_sign_token changes")
	}
}

func TestKeyBuilderKeepsSecretsInstanceScoped(t *testing.T) {
	t.Parallel()

	first := NewKeyBuilder("secret-a")
	second := NewKeyBuilder("secret-b")
	firstKey := first.BuildEmailVerifyKey(" Sensitive@Example.com ")
	secondKey := second.BuildEmailVerifyKey(" Sensitive@Example.com ")
	if firstKey == secondKey {
		t.Fatalf("keys should differ across independently configured builders")
	}
	if got := first.BuildEmailVerifyKey("sensitive@example.COM"); got != firstKey {
		t.Fatalf("normalization changed: %q != %q", got, firstKey)
	}
}

func TestBuildOAuth2DeviceKeys(t *testing.T) {
	t.Parallel()

	b := NewKeyBuilder("device-flow-test-secret")
	hx := b.hashExactIdentifier
	const (
		fid    = "0123456789abcdef0123456789abcdef"
		hdc    = "hdc_AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_AbCdEf"
		code   = "BCDFGHJK"
		handle = "dfh_AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_AbCdEf"
		cid    = "haruki-client"
		uid    = "12345"
	)
	cases := []struct {
		name string
		got  string
		want string
		raw  string
	}{
		{"flow", b.BuildOAuth2DeviceFlowKey(fid), "haruki:oauth2-device:flow:" + fid, ""},
		{"dc", b.BuildOAuth2DeviceCodeIndexKey(hdc), "haruki:oauth2-device:dc:" + hx("dc", hdc), hdc},
		{"uc", b.BuildOAuth2DeviceUserCodeIndexKey(code), "haruki:oauth2-device:uc:" + hx("uc", code), code},
		{"fh", b.BuildOAuth2DeviceFlowHandleIndexKey(handle), "haruki:oauth2-device:fh:" + hx("fh", handle), handle},
		{"unredeemed", b.BuildOAuth2DeviceUnredeemedKey(), "haruki:oauth2-device:unredeemed", ""},
		{"crid", b.BuildOAuth2DeviceConsentRequestKey(fid), "haruki:oauth2-device:crid:" + fid, ""},
		{"auth-attempt unknown", b.BuildOAuth2DeviceAuthAttemptUnknownClientKey(), "haruki:rate-limit:oauth2-device:auth-attempt:unknown-client", ""},
		{"auth-attempt client", b.BuildOAuth2DeviceAuthAttemptClientKey(cid), "haruki:rate-limit:oauth2-device:auth-attempt:client:" + hx("cid", cid), cid},
		{"auth-issued public", b.BuildOAuth2DeviceAuthIssuedPoolKey("public"), "haruki:rate-limit:oauth2-device:auth-issued:global:public", ""},
		{"auth-issued confidential", b.BuildOAuth2DeviceAuthIssuedPoolKey("confidential"), "haruki:rate-limit:oauth2-device:auth-issued:global:confidential", ""},
		{"auth-issued client", b.BuildOAuth2DeviceAuthIssuedClientKey(cid), "haruki:rate-limit:oauth2-device:auth-issued:client:" + hx("cid", cid), cid},
		{"lookup user", b.BuildOAuth2DeviceLookupUserKey(uid), "haruki:rate-limit:oauth2-device:lookup:user:" + hx("uid", uid), ""},
		{"lookup-fail user", b.BuildOAuth2DeviceLookupFailUserKey(uid), "haruki:rate-limit:oauth2-device:lookup-fail:user:" + hx("uid", uid), ""},
		{"lookup-fail user-day", b.BuildOAuth2DeviceLookupFailUserDayKey(uid), "haruki:rate-limit:oauth2-device:lookup-fail:user-day:" + hx("uid", uid), ""},
		{"lookup-fail global", b.BuildOAuth2DeviceLookupFailGlobalKey(), "haruki:rate-limit:oauth2-device:lookup-fail:global", ""},
		{"decision user-day", b.BuildOAuth2DeviceDecisionUserDayKey(uid), "haruki:rate-limit:oauth2-device:decision:user-day:" + hx("uid", uid), ""},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s key = %q, want %q", tc.name, tc.got, tc.want)
		}
		if tc.raw != "" && strings.Contains(tc.got, tc.raw) {
			t.Errorf("%s key %q contains the raw identifier", tc.name, tc.got)
		}
	}
}

func TestHashExactIdentifierKeepsCaseAndWhitespace(t *testing.T) {
	t.Parallel()

	for _, b := range []KeyBuilder{{}, NewKeyBuilder("device-flow-test-secret")} {
		if b.hashExactIdentifier("dc", "AbC") == b.hashExactIdentifier("dc", "abc") {
			t.Fatal("hashExactIdentifier must be case-sensitive")
		}
		if b.hashExactIdentifier("dc", " abc") == b.hashExactIdentifier("dc", "abc") {
			t.Fatal("hashExactIdentifier must not trim")
		}
		if b.hashExactIdentifier("dc", "abc") == b.hashExactIdentifier("uc", "abc") {
			t.Fatal("hashExactIdentifier must separate domains")
		}
		if b.HashOAuth2DeviceIdentifier("uc", "BCDFGHJK") != b.hashExactIdentifier("uc", "BCDFGHJK") {
			t.Fatal("HashOAuth2DeviceIdentifier must equal hashExactIdentifier")
		}
	}
	if NewKeyBuilder("secret-a-0123456789").hashExactIdentifier("dc", "abc") == NewKeyBuilder("secret-b-0123456789").hashExactIdentifier("dc", "abc") {
		t.Fatal("hashExactIdentifier must depend on the secret")
	}
}

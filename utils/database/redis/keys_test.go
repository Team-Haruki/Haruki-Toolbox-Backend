package redis

import "testing"

func TestBuildKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "email verify",
			got:  BuildEmailVerifyKey("a@example.com"),
			want: "haruki:email:verify:a@example.com",
		},
		{
			name: "reset password",
			got:  BuildResetPasswordKey("a@example.com"),
			want: "haruki:email:reset-password:a@example.com",
		},
		{
			name: "game account verify",
			got:  BuildGameAccountVerifyKey("u1", "jp", "123"),
			want: "haruki:game-account:verify:u1:jp:123",
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
			name: "otp attempt",
			got:  BuildOTPAttemptKey("a@example.com"),
			want: "haruki:email:attempt:a@example.com",
		},
		{
			name: "oauth2 auth code",
			got:  BuildOAuth2AuthCodeKey("code123"),
			want: "haruki:oauth2:code:code123",
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

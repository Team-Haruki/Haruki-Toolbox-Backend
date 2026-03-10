package userpasswordreset

import "testing"

func TestBuildResetPasswordURL(t *testing.T) {
	t.Parallel()

	url := buildResetPasswordURL("https://example.com/", "secret-1", "user+alias@example.com")
	expected := "https://example.com/user/reset-password/secret-1?email=user%2Balias%40example.com"
	if url != expected {
		t.Fatalf("url = %q, want %q", url, expected)
	}
}

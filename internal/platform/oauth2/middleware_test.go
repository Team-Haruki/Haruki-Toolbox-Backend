package oauth2

import "testing"

func TestBuildBearerChallenge(t *testing.T) {
	t.Parallel()

	if got := buildBearerChallenge("", "", ""); got != `Bearer realm="haruki-toolbox"` {
		t.Fatalf("challenge without error = %q", got)
	}

	got := buildBearerChallenge("invalid_token", `token "expired"`, "user:read")
	want := `Bearer realm="haruki-toolbox", error="invalid_token", error_description="token \"expired\"", scope="user:read"`
	if got != want {
		t.Fatalf("challenge with details = %q, want %q", got, want)
	}
}

func TestEscapeBearerAuthParam(t *testing.T) {
	t.Parallel()

	got := escapeBearerAuthParam(`a"b\c`)
	want := `a\"b\\c`
	if got != want {
		t.Fatalf("escapeBearerAuthParam = %q, want %q", got, want)
	}
}

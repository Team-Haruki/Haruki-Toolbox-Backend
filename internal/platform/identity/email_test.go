package identity

import "testing"

func TestNormalizeEmail(t *testing.T) {
	got := NormalizeEmail("  Foo.Bar+tag@Example.COM  ")
	want := "foo.bar+tag@example.com"
	if got != want {
		t.Fatalf("NormalizeEmail() = %q, want %q", got, want)
	}
}

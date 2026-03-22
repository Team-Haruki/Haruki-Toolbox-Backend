package upload

import "testing"

func TestParseIOSProxyPathInt(t *testing.T) {
	t.Parallel()

	t.Run("parse decimal id", func(t *testing.T) {
		t.Parallel()
		got, err := parseIOSProxyPathInt("08")
		if err != nil {
			t.Fatalf("parseIOSProxyPathInt returned error: %v", err)
		}
		if got != 8 {
			t.Fatalf("value = %d, want 8", got)
		}
	})

	t.Run("reject invalid id", func(t *testing.T) {
		t.Parallel()
		if _, err := parseIOSProxyPathInt("abc"); err == nil {
			t.Fatalf("expected parse error")
		}
	})
}

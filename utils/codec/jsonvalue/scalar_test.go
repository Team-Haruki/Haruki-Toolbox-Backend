package jsonvalue

import "testing"

func TestScalarString(t *testing.T) {
	for _, raw := range []string{"9007199254740993", "18446744073709551615", "1.5", "1e+25", "-0"} {
		got, ok := ScalarString([]byte(raw))
		if !ok || got != raw {
			t.Fatalf("%s: %q %v", raw, got, ok)
		}
	}
	if got, ok := ScalarString([]byte(`"a\nb"`)); !ok || got != "a\nb" {
		t.Fatal("string not unescaped")
	}
	for _, raw := range []string{`""`, "true", "null", "[]", "{}", "01", "1 2", "NaN", `"unterminated`} {
		if _, ok := ScalarString([]byte(raw)); ok {
			t.Fatalf("invalid scalar accepted: %s", raw)
		}
	}
}

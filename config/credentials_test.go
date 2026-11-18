package config

import "testing"

func TestIsExampleCredentialRecognizesShippedPlaceholderForms(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{" change-me-token ", true},
		{"bEaReR   change-me-token", true},
		{"Bearer fixture-token", false},
		{"", false},
	} {
		if got := IsExampleCredential(tc.value); got != tc.want {
			t.Errorf("IsExampleCredential(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}
